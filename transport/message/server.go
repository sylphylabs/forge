package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sylphylabs/forge/internal/backstop"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

var (
	// ErrNilSubscriber reports a server constructed without a subscriber.
	ErrNilSubscriber = errors.New("message: nil subscriber")
	// ErrNoBindings reports a server started without handlers.
	ErrNoBindings = errors.New("message: no bindings")
	// ErrAlreadyStarted reports a mutation after startup.
	ErrAlreadyStarted = errors.New("message: server already started")
	// ErrStopped reports a server that has already been stopped.
	ErrStopped = errors.New("message: server stopped")
	// ErrEmptyTopic reports an invalid destination.
	ErrEmptyTopic = errors.New("message: empty topic")
	// ErrNilHandler reports an invalid binding.
	ErrNilHandler = errors.New("message: nil handler")
	// ErrNilContext reports a nil lifecycle context.
	ErrNilContext = errors.New("message: nil context")
)

var _ transport.Server = (*Server)(nil)

// ServerOption configures a message Server before it starts.
type ServerOption func(*Server)

// WithMiddleware attaches server-wide middleware to every binding.
//
// The middleware is the same [middleware.UnaryMiddleware] HTTP and gRPC use, so
// recovery, logging, rate limiting, and the rest apply to a message consumer
// without a message-specific implementation of each. It is composed once,
// inside NewServer; a nil middleware, or one returning a nil handler, is
// reported by Start, the way a nil subscriber is.
func WithMiddleware(m ...middleware.UnaryMiddleware) ServerOption {
	return func(s *Server) {
		s.middleware = append(s.middleware, m...)
	}
}

// WithEndpoint sets the broker endpoint reported to middleware through
// transport.Transporter. It is descriptive only: the subscriber owns the
// actual connection.
func WithEndpoint(endpoint string) ServerOption {
	return func(s *Server) {
		s.endpoint = endpoint
	}
}

// WithShutdownTimeout bounds cleanup triggered by cancellation of Start's parent
// context. Explicit Stop callers provide their own context.
func WithShutdownTimeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

type binding struct {
	topic   string
	handler middleware.UnaryHandler
}

type serverState uint8

const (
	stateNew serverState = iota
	stateStarting
	stateRunning
	stateStopping
	stateStopped
)

// Server coordinates subscriptions and gives them the standard transport
// lifecycle. It owns every subscription created by Start and closes them in
// reverse registration order.
type Server struct {
	subscriber      Subscriber
	shutdownTimeout time.Duration
	endpoint        string

	mu       sync.Mutex
	state    serverState
	bindings []binding
	// middleware is filled by options during construction only; handler is
	// the chain composed once by NewServer, and err a composition failure
	// held for Start to report.
	middleware []middleware.UnaryMiddleware
	handler    middleware.UnaryHandler
	err        error
	subs       []Subscription
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// bindingKey carries the destination handler of one delivery through the
// server-wide middleware chain. The chain itself is composed once, in
// NewServer; only the bound handler of the current delivery travels through
// the context.
type bindingKey struct{}

// NewServer creates a message lifecycle coordinator. A nil subscriber or an
// invalid middleware chain is reported by Start so construction can remain
// side-effect free.
func NewServer(subscriber Subscriber, opts ...ServerOption) *Server {
	s := &Server{
		subscriber:      subscriber,
		shutdownTimeout: 10 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	s.handler, s.err = middleware.ComposeUnary(func(ctx context.Context, req any) (any, error) {
		next, ok := ctx.Value(bindingKey{}).(middleware.UnaryHandler)
		if !ok {
			return nil, fmt.Errorf("message: middleware severed the delivery from its handler")
		}
		return next(ctx, req)
	}, s.middleware...)
	return s
}

// Handle registers one destination handler. It must be called before Start.
func (s *Server) Handle(topic string, handler middleware.UnaryHandler) error {
	if strings.TrimSpace(topic) == "" {
		return ErrEmptyTopic
	}
	if handler == nil {
		return fmt.Errorf("%w for %q", ErrNilHandler, topic)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateNew {
		if s.state == stateStopped {
			return ErrStopped
		}
		return ErrAlreadyStarted
	}
	s.bindings = append(s.bindings, binding{topic: topic, handler: handler})
	return nil
}

// bound places one destination handler under the server-wide chain, with the
// transport backstop outside both: a panic anywhere below is logged with its
// stack and surfaces to the adapter as a generic internal error, never as an
// unwound worker.
func (s *Server) bound(handler middleware.UnaryHandler) middleware.UnaryHandler {
	return func(ctx context.Context, req any) (reply any, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				reply, err = nil, backstop.Recovered(ctx, "[Message]", rec)
			}
		}()
		return s.handler(context.WithValue(ctx, bindingKey{}, handler), req)
	}
}

// Start creates all subscriptions and waits until the server is stopped or
// its parent context is canceled.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	s.mu.Lock()
	if s.subscriber == nil {
		s.mu.Unlock()
		return ErrNilSubscriber
	}
	if s.err != nil {
		err := s.err
		s.mu.Unlock()
		return err
	}
	if len(s.bindings) == 0 {
		s.mu.Unlock()
		return ErrNoBindings
	}
	if s.state != stateNew {
		err := ErrAlreadyStarted
		if s.state == stateStopped {
			err = ErrStopped
		}
		s.mu.Unlock()
		return err
	}
	s.state = stateStarting
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	bindings := append([]binding(nil), s.bindings...)
	s.mu.Unlock()

	for _, b := range bindings {
		if runCtx.Err() != nil {
			return s.closeAfterCancellation(ctx)
		}
		handler := deliver(s.endpoint, s.bound(b.handler))
		sub, err := s.subscriber.Subscribe(runCtx, b.topic, handler)
		if err != nil {
			cleanupErr := s.closeAfterCancellation(ctx)
			return errors.Join(fmt.Errorf("subscribe %q: %w", b.topic, err), cleanupErr)
		}
		if sub == nil {
			cleanupErr := s.closeAfterCancellation(ctx)
			return errors.Join(fmt.Errorf("subscribe %q: nil subscription", b.topic), cleanupErr)
		}
		s.mu.Lock()
		if s.state != stateStarting || runCtx.Err() != nil {
			s.mu.Unlock()
			cleanupCtx, cancel := cleanupContext(ctx, s.shutdownTimeout)
			subErr := sub.Close(cleanupCtx)
			cancel()
			return errors.Join(subErr, s.closeAfterCancellation(ctx))
		}
		s.subs = append(s.subs, sub)
		s.mu.Unlock()
	}

	s.mu.Lock()
	if s.state == stateStarting {
		s.state = stateRunning
	}
	s.mu.Unlock()

	<-runCtx.Done()
	return s.closeAfterCancellation(ctx)
}

// Stop cancels delivery and closes subscriptions in reverse registration
// order. It is safe to call concurrently and repeatedly.
func (s *Server) Stop(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	return s.close(ctx)
}

func (s *Server) close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.state = stateStopping
		cancel := s.cancel
		subs := append([]Subscription(nil), s.subs...)
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		for i := len(subs) - 1; i >= 0; i-- {
			s.closeErr = errors.Join(s.closeErr, subs[i].Close(ctx))
		}
		s.mu.Lock()
		s.state = stateStopped
		s.mu.Unlock()
	})
	return s.closeErr
}

func (s *Server) closeAfterCancellation(parent context.Context) error {
	ctx, cancel := cleanupContext(parent, s.shutdownTimeout)
	defer cancel()
	return s.close(ctx)
}

func cleanupContext(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx := context.WithoutCancel(parent)
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
