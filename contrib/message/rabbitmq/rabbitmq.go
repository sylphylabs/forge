// Package rabbitmq adapts RabbitMQ (AMQP 0-9-1) to transport/message.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

const (
	// HeaderMessageKey carries message.Message.Key through an AMQP header.
	//
	// Key is published as the AMQP routing key, which is the only field a
	// RabbitMQ exchange routes on. The routing key is also the destination a
	// consumer sees, so a queue bound with a wildcard would otherwise be unable
	// to distinguish "the key the producer chose" from "the topic it was sent
	// to". Mirroring it into a header keeps Key round-tripping intact for
	// partition-style keys that are not themselves topics.
	HeaderMessageKey = "forge-message-key"

	// defaultPrefetch bounds unacknowledged deliveries per consumer. AMQP's
	// default is unlimited, which lets one consumer drain a queue into memory
	// and defeats round-robin dispatch across replicas.
	defaultPrefetch = 64
)

var (
	// ErrNilContext reports an operation started without a context.
	ErrNilContext = errors.New("rabbitmq: nil context")
	// ErrEmptyDestination reports an invalid message destination.
	ErrEmptyDestination = errors.New("rabbitmq: empty destination")
	// ErrEmptyURL reports an invalid AMQP server URL.
	ErrEmptyURL = errors.New("rabbitmq: empty url")
	// ErrNilMessage reports an invalid message.
	ErrNilMessage = errors.New("rabbitmq: nil message")
	// ErrNilHandler reports an invalid subscription handler.
	ErrNilHandler = errors.New("rabbitmq: nil handler")
	// ErrNilDialer reports an invalid connection option.
	ErrNilDialer = errors.New("rabbitmq: nil dialer")
	// ErrClosed reports an adapter closed by its owner.
	ErrClosed = errors.New("rabbitmq: client closed")
	// ErrBindingNotFound reports a destination with no configured queue.
	ErrBindingNotFound = errors.New("rabbitmq: binding not found")
	// ErrInvalidBinding reports an incomplete destination binding.
	ErrInvalidBinding = errors.New("rabbitmq: invalid binding")
	// ErrInvalidDisposition reports an unsupported handler error disposition.
	ErrInvalidDisposition = errors.New("rabbitmq: invalid error disposition")
	// ErrPublishNacked reports a publish the broker explicitly refused.
	ErrPublishNacked = errors.New("rabbitmq: publish nacked by broker")
	// ErrPublishReturned reports a mandatory publish no queue accepted.
	ErrPublishReturned = errors.New("rabbitmq: publish returned unroutable")
)

var (
	_ message.Publisher    = (*Client)(nil)
	_ message.Subscriber   = (*Client)(nil)
	_ message.Subscription = (*subscription)(nil)
)

// Channel is the subset of *amqp091.Channel the adapter uses. It exists so
// delivery, acknowledgement, and recovery behavior can be tested without a
// broker; production code always uses the real AMQP channel through amqpChannel.
//
// Publish returns a Confirmation rather than amqp091's *DeferredConfirmation
// because that type has no exported constructor and a zero value blocks
// forever, which would make the seam unimplementable outside amqp091.
type Channel interface {
	Publish(ctx context.Context, exchange, key string, mandatory bool, msg amqp.Publishing) (Confirmation, error)
	Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error)
	Qos(prefetchCount, prefetchSize int, global bool) error
	Confirm(noWait bool) error
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	NotifyClose(c chan *amqp.Error) chan *amqp.Error
	NotifyReturn(c chan amqp.Return) chan amqp.Return
	Close() error
}

// Confirmation is a pending publisher confirm. Wait reports whether the broker
// accepted the publish, or fails if ctx ends first.
type Confirmation interface {
	Wait(ctx context.Context) (bool, error)
}

// Dialer opens an AMQP connection. Reconnection calls it again, so it must be
// safe to call repeatedly and must not return a cached broken connection.
type Dialer func(context.Context) (Connection, error)

// Connection is the subset of *amqp091.Connection the adapter uses.
type Connection interface {
	Channel() (Channel, error)
	Close() error
}

// Disposition selects the AMQP outcome for a handler error.
type Disposition uint8

const (
	// Drop rejects the delivery without requeueing. RabbitMQ routes it to the
	// queue's dead-letter exchange when one is configured, and discards it
	// otherwise. It is the default because an immediate requeue of a
	// deterministically failing message spins the consumer at full speed.
	Drop Disposition = iota
	// Requeue returns the delivery to the front of its queue for redelivery.
	// It suits transient failures only, and needs a queue-level delivery-limit
	// or dead-letter policy to terminate.
	Requeue
)

// ErrorClassifier classifies a handler error without exposing AMQP types to
// application code.
type ErrorClassifier func(context.Context, *message.Message, error) Disposition

// FailureStage identifies where asynchronous processing failed.
type FailureStage string

const (
	StageConsume     FailureStage = "consume"
	StageHandler     FailureStage = "handler"
	StageAcknowledge FailureStage = "acknowledge"
	StageRecover     FailureStage = "recover"
)

// Failure describes an asynchronous consumer failure.
type Failure struct {
	Stage       FailureStage
	Destination string
	Message     *message.Message
	Err         error
}

// ErrorHandler observes failures that cannot be returned from Subscribe.
// Different subscriptions and deliveries may call it concurrently.
type ErrorHandler func(context.Context, Failure)

// Exchange describes an exchange the adapter may declare.
type Exchange struct {
	Name       string
	Kind       string
	Durable    bool
	AutoDelete bool
	Args       amqp.Table
}

// Queue describes a queue and its binding, used by the destination binding and
// by declaration when WithDeclare is set.
type Queue struct {
	Name       string
	Durable    bool
	AutoDelete bool
	Exclusive  bool
	Args       amqp.Table
	// BindingKeys are bound to Exchange. An empty slice binds nothing, which is
	// correct for a queue reached through the default exchange.
	BindingKeys []string
}

// Binding maps one message.Server destination to a queue to consume from, and
// to the exchange and routing key used when publishing to that destination.
type Binding struct {
	Queue    Queue
	Exchange Exchange
}

// Option configures a Client.
type Option func(*options) error

type options struct {
	url            string
	config         amqp.Config
	dialer         Dialer
	dialerSet      bool
	bindings       map[string]Binding
	declare        bool
	prefetch       int
	consumerPrefix string
	classify       ErrorClassifier
	onError        ErrorHandler
	persistent     bool
	mandatory      bool
	recoveryDelay  time.Duration
	returnTimeout  time.Duration
}

// WithURL sets the AMQP server URL used when the adapter owns the connection.
func WithURL(url string) Option {
	return func(o *options) error {
		o.url = url
		return nil
	}
}

// WithConfig sets the amqp091 dial configuration used with WithURL.
func WithConfig(config amqp.Config) Option {
	return func(o *options) error {
		o.config = config
		return nil
	}
}

// WithDialer replaces connection establishment. The adapter calls it again on
// every reconnection attempt and closes whatever it returns from Close.
func WithDialer(dialer Dialer) Option {
	return func(o *options) error {
		if dialer == nil {
			return ErrNilDialer
		}
		o.dialer = dialer
		o.dialerSet = true
		return nil
	}
}

// WithBindings maps message.Server destinations to queues and exchanges.
func WithBindings(bindings map[string]Binding) Option {
	return func(o *options) error {
		for destination, binding := range bindings {
			if strings.TrimSpace(destination) == "" {
				return fmt.Errorf("%w: empty destination", ErrInvalidBinding)
			}
			if strings.TrimSpace(binding.Queue.Name) == "" && strings.TrimSpace(binding.Exchange.Name) == "" {
				return fmt.Errorf("%w for %q: no queue and no exchange", ErrInvalidBinding, destination)
			}
			o.bindings[destination] = binding
		}
		return nil
	}
}

// WithDeclare makes the adapter declare the exchanges, queues, and bindings it
// was configured with. It is off by default: production topology is deployment
// state, and an application that declares it at startup can silently diverge
// from it or fail on a mismatched redeclaration. Enable it for tests and for
// deployments that own their own topology.
func WithDeclare(declare bool) Option {
	return func(o *options) error {
		o.declare = declare
		return nil
	}
}

// WithPrefetch sets basic.qos prefetch count per consumer. Zero restores AMQP's
// unlimited default, which is rarely what a Forge service wants.
func WithPrefetch(count int) Option {
	return func(o *options) error {
		o.prefetch = count
		return nil
	}
}

// WithConsumerPrefix prefixes generated consumer tags, making a consumer
// identifiable in RabbitMQ's management interface.
func WithConsumerPrefix(prefix string) Option {
	return func(o *options) error {
		o.consumerPrefix = prefix
		return nil
	}
}

// WithErrorClassifier classifies handler failures as Drop or Requeue.
func WithErrorClassifier(classifier ErrorClassifier) Option {
	return func(o *options) error {
		if classifier != nil {
			o.classify = classifier
		}
		return nil
	}
}

// WithErrorHandler observes handler, acknowledgement, consume, and recovery
// failures.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(o *options) error {
		o.onError = handler
		return nil
	}
}

// WithPersistentDelivery publishes with delivery mode 2 so a durable queue
// keeps messages across a broker restart. It is on by default; a durable queue
// with transient messages loses them silently.
func WithPersistentDelivery(persistent bool) Option {
	return func(o *options) error {
		o.persistent = persistent
		return nil
	}
}

// WithMandatoryPublish makes Publish fail when no queue accepted the message
// instead of discarding it at the exchange. It is on by default so a missing
// binding surfaces as an error rather than as lost messages.
func WithMandatoryPublish(mandatory bool) Option {
	return func(o *options) error {
		o.mandatory = mandatory
		return nil
	}
}

// WithRecoveryDelay sets the wait between reconnection attempts after the
// broker drops the connection.
func WithRecoveryDelay(delay time.Duration) Option {
	return func(o *options) error {
		o.recoveryDelay = delay
		return nil
	}
}

// WithReturnTimeout bounds how long Publish waits for a basic.return that would
// arrive just before the confirm. RabbitMQ sends the return first, but the two
// are separate frames, so a confirmed-but-unroutable publish needs a short
// grace period to be reported as ErrPublishReturned.
func WithReturnTimeout(timeout time.Duration) Option {
	return func(o *options) error {
		o.returnTimeout = timeout
		return nil
	}
}

// Client adapts one RabbitMQ connection to Forge message transport.
//
// It holds a single connection and opens one channel per subscription plus one
// shared publishing channel. AMQP channels are not safe for concurrent use by
// the frame writer's own accounting, so the publishing channel is serialized.
type Client struct {
	dialer         Dialer
	bindings       map[string]Binding
	declare        bool
	prefetch       int
	consumerPrefix string
	classify       ErrorClassifier
	onError        ErrorHandler
	persistent     bool
	mandatory      bool
	recoveryDelay  time.Duration
	returnTimeout  time.Duration

	mu       sync.Mutex
	conn     Connection
	pub      Channel
	returns  chan amqp.Return
	closed   bool
	consumer uint64
}

// New creates a RabbitMQ message adapter. It does not connect: the first
// Publish or Subscribe dials, so construction stays side-effect free and a
// broker that is not yet up does not fail application startup.
func New(opts ...Option) (*Client, error) {
	cfg := options{
		url:           "amqp://guest:guest@127.0.0.1:5672/",
		bindings:      make(map[string]Binding),
		prefetch:      defaultPrefetch,
		persistent:    true,
		mandatory:     true,
		recoveryDelay: time.Second,
		returnTimeout: 100 * time.Millisecond,
		classify: func(context.Context, *message.Message, error) Disposition {
			return Drop
		},
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}
	if !cfg.dialerSet {
		if strings.TrimSpace(cfg.url) == "" {
			return nil, ErrEmptyURL
		}
		url, config := cfg.url, cfg.config
		cfg.dialer = func(context.Context) (Connection, error) {
			conn, err := amqp.DialConfig(url, config)
			if err != nil {
				return nil, err
			}
			return &connection{conn: conn}, nil
		}
	}
	return &Client{
		dialer:         cfg.dialer,
		bindings:       cfg.bindings,
		declare:        cfg.declare,
		prefetch:       cfg.prefetch,
		consumerPrefix: cfg.consumerPrefix,
		classify:       cfg.classify,
		onError:        cfg.onError,
		persistent:     cfg.persistent,
		mandatory:      cfg.mandatory,
		recoveryDelay:  cfg.recoveryDelay,
		returnTimeout:  cfg.returnTimeout,
	}, nil
}

// Publish sends one message and waits for a publisher confirm.
//
// A successful return means the broker took responsibility for the message: it
// was routed to at least one queue and, for a durable queue with persistent
// delivery, persisted. It does not mean a consumer has processed it. A
// cancelled context after the frame is written leaves an ambiguous outcome, so
// callers still need idempotency keyed on Message.ID.
func (c *Client) Publish(ctx context.Context, destination string, msg *message.Message) error {
	if ctx == nil {
		return ErrNilContext
	}
	if strings.TrimSpace(destination) == "" {
		return ErrEmptyDestination
	}
	if msg == nil {
		return ErrNilMessage
	}

	exchange, key := c.route(destination, msg)
	channel, returns, err := c.publishChannel(ctx)
	if err != nil {
		return fmt.Errorf("rabbitmq: publish %q: %w", destination, err)
	}
	confirm, err := channel.Publish(ctx, exchange, key, c.mandatory, c.publishing(msg))
	if err != nil {
		c.discardPublishChannel(channel)
		return fmt.Errorf("rabbitmq: publish %q: %w", destination, err)
	}
	acked, err := confirm.Wait(ctx)
	if err != nil {
		return fmt.Errorf("rabbitmq: confirm %q: %w", destination, err)
	}
	if !acked {
		return fmt.Errorf("rabbitmq: publish %q: %w", destination, ErrPublishNacked)
	}
	if c.mandatory {
		if err := c.checkReturned(returns); err != nil {
			return fmt.Errorf("rabbitmq: publish %q: %w", destination, err)
		}
	}
	return nil
}

// Subscribe consumes the queue bound to destination and delivers to handler.
// The subscription lifetime is bound to ctx; cancellation cancels the AMQP
// consumer and closes its channel.
//
// Deliveries are acknowledged manually. A handler that returns nil acks; a
// handler that returns an error is nacked according to the error classifier.
// Handlers run one at a time per subscription, so prefetch bounds buffering
// rather than concurrency; run several subscriptions for parallelism.
func (c *Client) Subscribe(ctx context.Context, destination string, handler message.Handler) (message.Subscription, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(destination) == "" {
		return nil, ErrEmptyDestination
	}
	if handler == nil {
		return nil, ErrNilHandler
	}
	binding, ok := c.bindings[destination]
	if !ok {
		return nil, fmt.Errorf("%w for %q", ErrBindingNotFound, destination)
	}
	if strings.TrimSpace(binding.Queue.Name) == "" {
		return nil, fmt.Errorf("%w for %q: no queue to consume", ErrInvalidBinding, destination)
	}

	// The first consumer attempt is synchronous so a missing queue or a broker
	// that is down fails Subscribe, and message.Server can roll back the
	// server start. Only failures after this point are recovered in the loop.
	channel, deliveries, err := c.consume(ctx, binding)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: subscribe %q: %w", destination, err)
	}

	sub := &subscription{done: make(chan struct{}), stopped: make(chan struct{})}
	go c.run(ctx, sub, destination, binding, handler, channel, deliveries)
	return sub, nil
}

// Close closes the adapter's connection and stops recovery. Subscriptions
// created from it stop delivering.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn, pub := c.conn, c.pub
	c.conn, c.pub, c.returns = nil, nil, nil
	c.mu.Unlock()

	var err error
	if pub != nil {
		err = ignoreClosed(pub.Close())
	}
	if conn != nil {
		err = errors.Join(err, ignoreClosed(conn.Close()))
	}
	return err
}

// ignoreClosed drops "already closed" errors. The broker closes channels and
// connections on its own — a restart, a channel-level protocol error, an idle
// timeout — so a shutdown that finds them gone has still succeeded, and
// reporting it would make every such Close look like a failure.
func ignoreClosed(err error) error {
	if err == nil || errors.Is(err, amqp.ErrClosed) {
		return nil
	}
	var amqpErr *amqp.Error
	if errors.As(err, &amqpErr) && amqpErr.Code == amqp.ChannelError {
		return nil
	}
	return err
}

// run owns one subscription's delivery loop and its recovery across broker
// restarts. It exits only when the subscription context is cancelled, the
// subscription is closed, or the client is closed.
func (c *Client) run(ctx context.Context, sub *subscription, destination string, binding Binding, handler message.Handler, channel Channel, deliveries <-chan amqp.Delivery) {
	defer close(sub.stopped)
	for {
		closed := channel.NotifyClose(make(chan *amqp.Error, 1))
		reconnect := c.drain(ctx, sub, destination, handler, deliveries, closed)
		// Closing the channel of a connection the broker already dropped fails
		// harmlessly; the error carries no information the caller can act on.
		_ = channel.Close()
		if !reconnect {
			return
		}

		next, nextDeliveries, err := c.reconnect(ctx, sub, destination, binding)
		if err != nil {
			return
		}
		channel, deliveries = next, nextDeliveries
	}
}

// drain reports whether the loop should re-establish the consumer.
func (c *Client) drain(ctx context.Context, sub *subscription, destination string, handler message.Handler, deliveries <-chan amqp.Delivery, closed <-chan *amqp.Error) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-sub.done:
			return false
		case err := <-closed:
			c.report(ctx, Failure{Stage: StageConsume, Destination: destination, Err: channelError(err)})
			return true
		case delivery, ok := <-deliveries:
			if !ok {
				// A closed delivery channel without a channel error is a
				// broker-side basic.cancel, which a queue deletion also
				// produces. Recovery re-declares when configured.
				return true
			}
			c.handle(ctx, destination, handler, delivery)
		}
	}
}

// reconnect retries the consumer until it succeeds or the subscription ends.
func (c *Client) reconnect(ctx context.Context, sub *subscription, destination string, binding Binding) (Channel, <-chan amqp.Delivery, error) {
	timer := time.NewTimer(c.recoveryDelay)
	defer timer.Stop()
	for {
		timer.Reset(c.recoveryDelay)
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-sub.done:
			return nil, nil, ErrClosed
		case <-timer.C:
		}

		c.dropConnection()
		channel, deliveries, err := c.consume(ctx, binding)
		if err == nil {
			return channel, deliveries, nil
		}
		c.report(ctx, Failure{Stage: StageRecover, Destination: destination, Err: err})
		if errors.Is(err, ErrClosed) {
			return nil, nil, err
		}
	}
}

// handle runs one delivery to completion and settles it. Acknowledgement is
// always the adapter's decision: message.Handler returns an error, and the core
// contract deliberately leaves the AMQP outcome here.
func (c *Client) handle(ctx context.Context, destination string, handler message.Handler, delivery amqp.Delivery) {
	msg := fromDelivery(delivery)
	handlerCtx := metadata.NewServerContext(ctx, msg.Headers.Clone())
	handlerErr := handler(handlerCtx, delivery.RoutingKey, msg)
	if handlerErr == nil {
		if err := delivery.Ack(false); err != nil {
			c.report(handlerCtx, Failure{
				Stage:       StageAcknowledge,
				Destination: destination,
				Message:     msg.Clone(),
				Err:         err,
			})
		}
		return
	}

	var settleErr error
	switch c.classify(handlerCtx, msg, handlerErr) {
	case Drop:
		settleErr = delivery.Nack(false, false)
	case Requeue:
		settleErr = delivery.Nack(false, true)
	default:
		// An unknown disposition must not leave the delivery unacknowledged:
		// it would hold a prefetch slot until the channel closes.
		settleErr = errors.Join(ErrInvalidDisposition, delivery.Nack(false, false))
	}
	c.report(handlerCtx, Failure{
		Stage:       StageHandler,
		Destination: destination,
		Message:     msg.Clone(),
		Err:         errors.Join(handlerErr, settleErr),
	})
}

func (c *Client) consume(ctx context.Context, binding Binding) (Channel, <-chan amqp.Delivery, error) {
	channel, err := c.channel(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := c.declareTopology(channel, binding); err != nil {
		return nil, nil, errors.Join(err, channel.Close())
	}
	// Prefetch is per consumer, so it is applied to the subscription's own
	// channel rather than globally across the connection.
	if err := channel.Qos(c.prefetch, 0, false); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("qos: %w", err), channel.Close())
	}
	deliveries, err := channel.Consume(binding.Queue.Name, c.consumerTag(), false, binding.Queue.Exclusive, false, false, nil)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("consume %q: %w", binding.Queue.Name, err), channel.Close())
	}
	return channel, deliveries, nil
}

// declareTopology declares the binding's exchange, queue, and bindings when the
// adapter was asked to own topology.
func (c *Client) declareTopology(channel Channel, binding Binding) error {
	if !c.declare {
		return nil
	}
	// The default exchange always exists and cannot be declared.
	if binding.Exchange.Name != "" {
		kind := binding.Exchange.Kind
		if kind == "" {
			kind = amqp.ExchangeTopic
		}
		if err := channel.ExchangeDeclare(binding.Exchange.Name, kind, binding.Exchange.Durable, binding.Exchange.AutoDelete, false, false, binding.Exchange.Args); err != nil {
			return fmt.Errorf("declare exchange %q: %w", binding.Exchange.Name, err)
		}
	}
	if binding.Queue.Name == "" {
		return nil
	}
	if _, err := channel.QueueDeclare(binding.Queue.Name, binding.Queue.Durable, binding.Queue.AutoDelete, binding.Queue.Exclusive, false, binding.Queue.Args); err != nil {
		return fmt.Errorf("declare queue %q: %w", binding.Queue.Name, err)
	}
	if binding.Exchange.Name == "" {
		return nil
	}
	for _, key := range binding.Queue.BindingKeys {
		if err := channel.QueueBind(binding.Queue.Name, key, binding.Exchange.Name, false, nil); err != nil {
			return fmt.Errorf("bind queue %q to %q: %w", binding.Queue.Name, binding.Exchange.Name, err)
		}
	}
	return nil
}

// route resolves the exchange and routing key for a destination. An unbound
// destination publishes through the default exchange, where the routing key is
// a queue name, so plain queue names keep working without configuration.
func (c *Client) route(destination string, msg *message.Message) (exchange, key string) {
	key = destination
	if binding, ok := c.bindings[destination]; ok {
		exchange = binding.Exchange.Name
	}
	if msg.Key != "" {
		key = msg.Key
	}
	return exchange, key
}

// publishChannel returns the shared publishing channel in confirm mode.
func (c *Client) publishChannel(ctx context.Context) (Channel, <-chan amqp.Return, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if c.pub != nil {
		channel, returns := c.pub, c.returns
		c.mu.Unlock()
		return channel, returns, nil
	}
	c.mu.Unlock()

	channel, err := c.channel(ctx)
	if err != nil {
		return nil, nil, err
	}
	// Confirm mode is not optional: without it Publish could only report that
	// the frame was written locally, which the contract asks adapters to avoid
	// presenting as success.
	if err := channel.Confirm(false); err != nil {
		return nil, nil, errors.Join(fmt.Errorf("confirm mode: %w", err), channel.Close())
	}
	returns := channel.NotifyReturn(make(chan amqp.Return, 1))

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, nil, errors.Join(ErrClosed, channel.Close())
	}
	// A concurrent call already installed a publishing channel. Keep it and
	// discard this one; failing to close a channel nobody will use is not an
	// error the caller can act on.
	if c.pub != nil {
		_ = channel.Close()
		return c.pub, c.returns, nil
	}
	c.pub, c.returns = channel, returns
	return channel, returns, nil
}

// discardPublishChannel drops a publishing channel the broker has invalidated.
// AMQP closes the whole channel on a protocol error, so it cannot be reused.
func (c *Client) discardPublishChannel(channel Channel) {
	c.mu.Lock()
	if c.pub != channel {
		c.mu.Unlock()
		return
	}
	c.pub, c.returns = nil, nil
	c.mu.Unlock()
	_ = channel.Close()
}

// checkReturned reports a confirmed publish that no queue accepted. RabbitMQ
// sends basic.return before the confirm, but they are distinct frames, so a
// bounded wait avoids both a false negative and a stall.
func (c *Client) checkReturned(returns <-chan amqp.Return) error {
	if returns == nil {
		return nil
	}
	if c.returnTimeout <= 0 {
		select {
		case returned := <-returns:
			return fmt.Errorf("%w: %d %s", ErrPublishReturned, returned.ReplyCode, returned.ReplyText)
		default:
			return nil
		}
	}
	timer := time.NewTimer(c.returnTimeout)
	defer timer.Stop()
	select {
	case returned := <-returns:
		return fmt.Errorf("%w: %d %s", ErrPublishReturned, returned.ReplyCode, returned.ReplyText)
	case <-timer.C:
		return nil
	}
}

// channel opens a channel on the shared connection, dialing if needed.
func (c *Client) channel(ctx context.Context) (Channel, error) {
	conn, err := c.connection(ctx)
	if err != nil {
		return nil, err
	}
	channel, err := conn.Channel()
	if err != nil {
		// A connection that cannot open a channel is usually already dead;
		// dropping it lets the next attempt redial rather than retry a corpse.
		c.dropConnection()
		return nil, fmt.Errorf("open channel: %w", err)
	}
	return channel, nil
}

func (c *Client) connection(ctx context.Context) (Connection, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.conn != nil {
		conn := c.conn
		c.mu.Unlock()
		return conn, nil
	}
	c.mu.Unlock()

	conn, err := c.dialer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	if conn == nil {
		return nil, ErrNilDialer
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.Join(ErrClosed, conn.Close())
	}
	// A concurrent dial already installed a connection; keep it and close this
	// one so the client never holds more than one.
	if c.conn != nil {
		_ = conn.Close()
		return c.conn, nil
	}
	c.conn = conn
	return conn, nil
}

// dropConnection discards the shared connection so the next operation redials.
func (c *Client) dropConnection() {
	c.mu.Lock()
	conn, pub := c.conn, c.pub
	c.conn, c.pub, c.returns = nil, nil, nil
	c.mu.Unlock()
	if pub != nil {
		_ = pub.Close()
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *Client) consumerTag() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consumer++
	prefix := c.consumerPrefix
	if prefix == "" {
		prefix = "forge"
	}
	return fmt.Sprintf("%s-%d", prefix, c.consumer)
}

func (c *Client) report(ctx context.Context, failure Failure) {
	if c.onError != nil {
		c.onError(ctx, failure)
	}
}

func (c *Client) publishing(msg *message.Message) amqp.Publishing {
	publishing := amqp.Publishing{
		Headers:   toTable(msg),
		MessageId: msg.ID,
		Body:      slices.Clone(msg.Body),
	}
	if c.persistent {
		publishing.DeliveryMode = amqp.Persistent
	}
	return publishing
}

type subscription struct {
	once    sync.Once
	done    chan struct{}
	stopped chan struct{}
}

// Close stops delivery and waits for the in-flight handler to settle, so a
// message is never left unacknowledged by a shutdown the caller believes
// completed. It is idempotent.
func (s *subscription) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	s.once.Do(func() { close(s.done) })
	select {
	case <-s.stopped:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// connection adapts *amqp091.Connection to the Connection seam.
type connection struct {
	conn *amqp.Connection
}

func (c *connection) Channel() (Channel, error) {
	channel, err := c.conn.Channel()
	if err != nil {
		return nil, err
	}
	return &amqpChannel{Channel: channel}, nil
}

func (c *connection) Close() error { return c.conn.Close() }

// amqpChannel adapts *amqp091.Channel to the Channel seam. Only Publish needs
// reshaping; the remaining methods match amqp091 exactly and are promoted.
type amqpChannel struct {
	*amqp.Channel
}

func (c *amqpChannel) Publish(ctx context.Context, exchange, key string, mandatory bool, msg amqp.Publishing) (Confirmation, error) {
	confirm, err := c.Channel.PublishWithDeferredConfirmWithContext(ctx, exchange, key, mandatory, false, msg)
	if err != nil {
		return nil, err
	}
	return deferredConfirmation{confirm}, nil
}

type deferredConfirmation struct {
	confirm *amqp.DeferredConfirmation
}

func (d deferredConfirmation) Wait(ctx context.Context) (bool, error) {
	return d.confirm.WaitContext(ctx)
}

// toTable converts portable headers to an AMQP headers table. Multi-value
// headers become a []any so metadata's multiplicity survives a round trip;
// AMQP field tables have no repeated-key form.
func toTable(msg *message.Message) amqp.Table {
	table := make(amqp.Table, len(msg.Headers)+1)
	for key, values := range msg.Headers {
		switch len(values) {
		case 0:
		case 1:
			table[key] = values[0]
		default:
			boxed := make([]any, len(values))
			for i, value := range values {
				boxed[i] = value
			}
			table[key] = boxed
		}
	}
	if msg.Key != "" {
		table[HeaderMessageKey] = msg.Key
	}
	return table
}

func fromDelivery(delivery amqp.Delivery) *message.Message {
	msg := message.New(delivery.Body)
	msg.ID = delivery.MessageId
	msg.Headers = metadata.Metadata{}
	for key, value := range delivery.Headers {
		switch typed := value.(type) {
		case string:
			msg.Headers.Add(key, typed)
		case []byte:
			msg.Headers.Add(key, string(typed))
		case []any:
			for _, element := range typed {
				msg.Headers.Add(key, headerString(element))
			}
		default:
			msg.Headers.Add(key, headerString(value))
		}
	}
	msg.Key = msg.Headers.Get(HeaderMessageKey)
	if msg.Key == "" {
		msg.Key = delivery.RoutingKey
	}
	return msg
}

// headerString renders a non-string AMQP field value. Producers outside Forge
// may send numbers or booleans, and dropping them would lose information a
// handler can still use.
func headerString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func channelError(err *amqp.Error) error {
	if err == nil {
		return errors.New("channel closed")
	}
	return err
}
