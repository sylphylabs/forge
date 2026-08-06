// Package nats adapts core NATS publish/subscribe to transport/message.
package nats

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/openkratos/kratos/metadata"
	"github.com/openkratos/kratos/transport/message"
)

const (
	// HeaderMessageID carries message.Message.ID through core NATS headers.
	HeaderMessageID = "openkratos-message-id"
	// HeaderMessageKey carries message.Message.Key through core NATS headers.
	HeaderMessageKey = "openkratos-message-key"
)

var (
	// ErrNilContext reports an operation started without a context.
	ErrNilContext = errors.New("nats: nil context")
	// ErrEmptySubject reports an invalid NATS subject.
	ErrEmptySubject = errors.New("nats: empty subject")
	// ErrEmptyURL reports an invalid NATS server URL.
	ErrEmptyURL = errors.New("nats: empty url")
	// ErrNilMessage reports an invalid message.
	ErrNilMessage = errors.New("nats: nil message")
	// ErrNilHandler reports an invalid subscription handler.
	ErrNilHandler = errors.New("nats: nil handler")
	// ErrNilConn reports an invalid connection option.
	ErrNilConn = errors.New("nats: nil connection")
	// ErrClosed reports an adapter closed by its owner.
	ErrClosed = errors.New("nats: client closed")
)

var (
	_ message.Publisher    = (*Client)(nil)
	_ message.Subscriber   = (*Client)(nil)
	_ message.Subscription = (*subscription)(nil)
)

// ErrorHandler observes handler failures from asynchronous NATS callbacks.
// Core NATS has no acknowledgement decision to return to the server, so the
// adapter reports the error to the application instead of logging globally.
// Different subscriptions may call the handler concurrently.
type ErrorHandler func(context.Context, string, *message.Message, error)

// Option configures a Client.
type Option func(*options)

type options struct {
	url            string
	conn           *natsgo.Conn
	connSet        bool
	connectOptions []natsgo.Option
	queue          string
	errorHandler   ErrorHandler
	flushTimeout   time.Duration
}

// WithURL sets the NATS server URL used when the adapter owns the connection.
func WithURL(url string) Option {
	return func(o *options) {
		o.url = url
	}
}

// WithConn uses an application-owned NATS connection. Client.Close will not
// close a connection supplied this way.
func WithConn(conn *natsgo.Conn) Option {
	return func(o *options) {
		o.conn = conn
		o.connSet = true
	}
}

// WithConnectOptions appends options used by nats.Connect.
func WithConnectOptions(opts ...natsgo.Option) Option {
	return func(o *options) {
		o.connectOptions = append(o.connectOptions, opts...)
	}
}

// WithQueue subscribes through a NATS queue group.
func WithQueue(queue string) Option {
	return func(o *options) {
		o.queue = queue
	}
}

// WithErrorHandler observes asynchronous handler failures.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(o *options) {
		o.errorHandler = handler
	}
}

// WithFlushTimeout sets the default FlushWithContext deadline used by Publish
// and Subscribe when the caller's context has no deadline. A non-positive value
// leaves the caller's context unchanged.
func WithFlushTimeout(timeout time.Duration) Option {
	return func(o *options) {
		o.flushTimeout = timeout
	}
}

// Client adapts one NATS connection to OpenKratos message transport.
type Client struct {
	conn         *natsgo.Conn
	ownsConn     bool
	queue        string
	errorHandler ErrorHandler
	flushTimeout time.Duration

	mu     sync.RWMutex
	closed bool
}

// New creates a NATS message adapter. Without WithConn, the returned client
// owns the connection and closes it from Close.
func New(opts ...Option) (*Client, error) {
	cfg := options{
		url:          natsgo.DefaultURL,
		flushTimeout: 10 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.connSet {
		if cfg.conn == nil {
			return nil, ErrNilConn
		}
		return &Client{
			conn:         cfg.conn,
			queue:        cfg.queue,
			errorHandler: cfg.errorHandler,
			flushTimeout: cfg.flushTimeout,
		}, nil
	}
	if strings.TrimSpace(cfg.url) == "" {
		return nil, ErrEmptyURL
	}
	conn, err := natsgo.Connect(cfg.url, cfg.connectOptions...)
	if err != nil {
		return nil, fmt.Errorf("nats: connect: %w", err)
	}
	return &Client{
		conn:         conn,
		ownsConn:     true,
		queue:        cfg.queue,
		errorHandler: cfg.errorHandler,
		flushTimeout: cfg.flushTimeout,
	}, nil
}

// Publish sends one core NATS message and flushes the connection with the
// caller's context. A successful return means the server responded to the flush
// after the publish; it does not imply durable JetStream storage.
func (c *Client) Publish(ctx context.Context, subject string, msg *message.Message) error {
	if ctx == nil {
		return ErrNilContext
	}
	if strings.TrimSpace(subject) == "" {
		return ErrEmptySubject
	}
	if msg == nil {
		return ErrNilMessage
	}
	conn, err := c.connection()
	if err != nil {
		return err
	}
	if err := conn.PublishMsg(toNATSMsg(subject, msg)); err != nil {
		return fmt.Errorf("nats: publish %q: %w", subject, err)
	}
	if err := c.flush(conn, ctx); err != nil {
		return fmt.Errorf("nats: flush publish %q: %w", subject, err)
	}
	return nil
}

// Request sends a NATS request/reply message. It is intentionally adapter
// specific; request/reply is not part of the broker-neutral message contract.
func (c *Client) Request(ctx context.Context, subject string, msg *message.Message) (*message.Message, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(subject) == "" {
		return nil, ErrEmptySubject
	}
	if msg == nil {
		return nil, ErrNilMessage
	}
	conn, err := c.connection()
	if err != nil {
		return nil, err
	}
	reply, err := conn.RequestMsgWithContext(ctx, toNATSMsg(subject, msg))
	if err != nil {
		return nil, fmt.Errorf("nats: request %q: %w", subject, err)
	}
	return fromNATSMsg(reply), nil
}

// Subscribe registers a core NATS subscription. The subscription lifetime is
// bound to ctx; cancellation unsubscribes and stops later deliveries.
func (c *Client) Subscribe(ctx context.Context, subject string, handler message.Handler) (message.Subscription, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(subject) == "" {
		return nil, ErrEmptySubject
	}
	if handler == nil {
		return nil, ErrNilHandler
	}
	conn, err := c.connection()
	if err != nil {
		return nil, fmt.Errorf("nats: subscribe %q: %w", subject, err)
	}
	callback := func(natsMsg *natsgo.Msg) {
		msg := fromNATSMsg(natsMsg)
		handlerCtx := metadata.NewServerContext(ctx, msg.Headers.Clone())
		if err := handler(handlerCtx, natsMsg.Subject, msg); err != nil && c.errorHandler != nil {
			c.errorHandler(handlerCtx, natsMsg.Subject, msg.Clone(), err)
		}
	}
	var sub *natsgo.Subscription
	if c.queue == "" {
		sub, err = conn.Subscribe(subject, callback)
	} else {
		sub, err = conn.QueueSubscribe(subject, c.queue, callback)
	}
	if err != nil {
		return nil, fmt.Errorf("nats: subscribe %q: %w", subject, err)
	}
	if err := c.flush(conn, ctx); err != nil {
		_ = sub.Unsubscribe()
		return nil, fmt.Errorf("nats: subscribe %q: %w", subject, err)
	}
	return newSubscription(ctx, sub), nil
}

// Close closes an adapter-owned connection. Application-owned connections
// supplied with WithConn are left open.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	if c.ownsConn {
		c.conn.Close()
	}
	return nil
}

func (c *Client) connection() (*natsgo.Conn, error) {
	if c == nil {
		return nil, ErrNilConn
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.conn == nil {
		return nil, ErrNilConn
	}
	if c.closed {
		return nil, ErrClosed
	}
	return c.conn, nil
}

func (c *Client) flush(conn *natsgo.Conn, ctx context.Context) error {
	flushCtx, cancel := c.flushContext(ctx)
	defer cancel()
	return conn.FlushWithContext(flushCtx)
}

func (c *Client) flushContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.flushTimeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.flushTimeout)
}

type subscription struct {
	sub     *natsgo.Subscription
	once    sync.Once
	done    chan struct{}
	stopped chan struct{}
	err     error
}

func newSubscription(ctx context.Context, sub *natsgo.Subscription) *subscription {
	s := &subscription{
		sub:     sub,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go func() {
		defer close(s.stopped)
		select {
		case <-ctx.Done():
			s.close()
		case <-s.done:
		}
	}()
	return s
}

func (s *subscription) Close(context.Context) error {
	err := s.close()
	<-s.stopped
	return err
}

func (s *subscription) close() error {
	s.once.Do(func() {
		s.err = s.sub.Unsubscribe()
		close(s.done)
	})
	return s.err
}

func toNATSMsg(subject string, msg *message.Message) *natsgo.Msg {
	natsMsg := natsgo.NewMsg(subject)
	natsMsg.Data = slices.Clone(msg.Body)
	for key, values := range msg.Headers {
		for _, value := range values {
			natsMsg.Header.Add(key, value)
		}
	}
	if msg.ID != "" {
		natsMsg.Header.Set(HeaderMessageID, msg.ID)
	}
	if msg.Key != "" {
		natsMsg.Header.Set(HeaderMessageKey, msg.Key)
	}
	return natsMsg
}

func fromNATSMsg(natsMsg *natsgo.Msg) *message.Message {
	msg := message.New(natsMsg.Data)
	msg.ID = natsMsg.Header.Get(HeaderMessageID)
	msg.Key = natsMsg.Header.Get(HeaderMessageKey)
	msg.Headers = metadata.Metadata{}
	for key, values := range natsMsg.Header {
		for _, value := range values {
			msg.Headers.Add(key, value)
		}
	}
	return msg
}
