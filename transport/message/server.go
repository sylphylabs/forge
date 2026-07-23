package message

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/openkratos/kratos/transport"
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

// WithMiddleware adds typed middleware to every binding.
func WithMiddleware(m ...Middleware) ServerOption {
	return func(s *Server) {
		for _, mw := range m {
			if mw != nil {
				s.middleware = append(s.middleware, mw)
			}
		}
	}
}

// ShutdownTimeout bounds cleanup triggered by cancellation of Start's parent
// context. Explicit Stop callers provide their own context.
func ShutdownTimeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.shutdownTimeout = timeout
	}
}

type binding struct {
	topic   string
	handler Handler
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

	mu         sync.Mutex
	state      serverState
	bindings   []binding
	middleware []Middleware
	subs       []Subscription
	cancel     context.CancelFunc
	closeOnce  sync.Once
	closeErr   error
}

// NewServer creates a message lifecycle coordinator. A nil subscriber is
// reported by Start so construction can remain side-effect free.
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
	return s
}

// Handle registers one destination handler. It must be called before Start.
func (s *Server) Handle(topic string, handler Handler) error {
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

// Use adds typed middleware before Start.
func (s *Server) Use(m ...Middleware) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != stateNew {
		if s.state == stateStopped {
			return ErrStopped
		}
		return ErrAlreadyStarted
	}
	for _, mw := range m {
		if mw != nil {
			s.middleware = append(s.middleware, mw)
		}
	}
	return nil
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
	middleware := append([]Middleware(nil), s.middleware...)
	s.mu.Unlock()

	wrapped := Chain(middleware...)
	for _, b := range bindings {
		if runCtx.Err() != nil {
			return s.closeAfterCancellation(ctx)
		}
		handler := wrapped(b.handler)
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
