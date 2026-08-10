// Package mqtt adapts MQTT 5 publish/subscribe to transport/message.
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

const (
	// PropertyMessageKey carries message.Message.Key as an MQTT 5 user
	// property. MQTT has no native partition key: the topic is the only
	// routing dimension, and ordering is per-topic per-session rather than
	// per-key. Mapping Key onto a user property keeps it a portable label that
	// survives a round trip, without implying the partitioning or ordering
	// guarantees that a Kafka key would carry.
	PropertyMessageKey = "forge-message-key"
	// PropertyMessageID carries message.Message.ID when the adapter is
	// configured with WithIDInUserProperty instead of the default Correlation
	// Data mapping.
	PropertyMessageID = "forge-message-id"
)

var (
	// ErrNilContext reports an operation started without a context.
	ErrNilContext = errors.New("mqtt: nil context")
	// ErrEmptyTopic reports an invalid MQTT topic.
	ErrEmptyTopic = errors.New("mqtt: empty topic")
	// ErrEmptyURL reports an invalid MQTT broker URL.
	ErrEmptyURL = errors.New("mqtt: empty url")
	// ErrNilMessage reports an invalid message.
	ErrNilMessage = errors.New("mqtt: nil message")
	// ErrNilHandler reports an invalid subscription handler.
	ErrNilHandler = errors.New("mqtt: nil handler")
	// ErrNilConn reports an invalid connection option.
	ErrNilConn = errors.New("mqtt: nil connection")
	// ErrClosed reports an adapter closed by its owner.
	ErrClosed = errors.New("mqtt: client closed")
	// ErrInvalidQoS reports a QoS outside the 0..2 range defined by MQTT.
	ErrInvalidQoS = errors.New("mqtt: invalid qos")
	// ErrWildcardPublish reports a publish to a topic filter. MQTT forbids
	// wildcards in a PUBLISH topic name, so this fails locally rather than
	// letting the broker close the connection.
	ErrWildcardPublish = errors.New("mqtt: wildcard in publish topic")
	// ErrSubscribeRejected reports a SUBACK whose reason code denied the
	// subscription. The broker answers each filter individually, so a
	// transport-level success can still carry an authorization failure.
	ErrSubscribeRejected = errors.New("mqtt: subscription rejected by broker")
	// ErrPublishRejected reports a PUBACK or PUBCOMP whose reason code
	// rejected the publication at QoS 1 or 2.
	ErrPublishRejected = errors.New("mqtt: publish rejected by broker")
)

var (
	_ message.Publisher    = (*Client)(nil)
	_ message.Subscriber   = (*Client)(nil)
	_ message.Subscription = (*subscription)(nil)
)

// connection is the subset of autopaho.ConnectionManager the adapter uses. It
// exists so delivery, acknowledgement, and lifecycle behaviour can be tested
// without a broker; production code always passes the real manager.
type connection interface {
	Publish(context.Context, *paho.Publish) (*paho.PublishResponse, error)
	Subscribe(context.Context, *paho.Subscribe) (*paho.Suback, error)
	Unsubscribe(context.Context, *paho.Unsubscribe) (*paho.Unsuback, error)
	Disconnect(context.Context) error
	AwaitConnection(context.Context) error
}

// ErrorHandler observes handler failures from asynchronous MQTT deliveries.
// A handler error cannot be returned to an MQTT broker, so the adapter reports
// it to the application instead of logging globally. Different subscriptions
// may call the handler concurrently.
type ErrorHandler func(context.Context, string, *message.Message, error)

// Option configures a Client.
type Option func(*options)

type options struct {
	urls          []string
	conn          connection
	connSet       bool
	clientID      string
	publishQoS    byte
	subscribeQoS  byte
	retain        bool
	cleanStart    bool
	sessionExpiry uint32
	keepAlive     uint16
	idInUserProp  bool
	ackOnError    bool
	errorHandler  ErrorHandler
	connectWait   time.Duration
	ackInterval   time.Duration
	configure     func(*autopaho.ClientConfig)
}

// WithURL sets the broker URLs used when the adapter owns the connection.
// Multiple URLs are tried in order on each connection attempt.
func WithURL(urls ...string) Option {
	return func(o *options) {
		o.urls = append(o.urls, urls...)
	}
}

// WithClientID sets the MQTT client identifier. An empty value asks the broker
// to assign one, which is only usable with a clean start: an assigned
// identifier cannot be reused to resume a session after reconnect.
func WithClientID(id string) Option {
	return func(o *options) {
		o.clientID = id
	}
}

// WithConnectionManager uses an application-owned autopaho connection manager.
// Close will not disconnect a manager supplied this way.
func WithConnectionManager(cm *autopaho.ConnectionManager) Option {
	return func(o *options) {
		// A typed nil pointer would satisfy the interface and defer the
		// failure to first use, so it is normalized to a nil interface here
		// and reported by New.
		if cm == nil {
			o.conn = nil
		} else {
			o.conn = cm
		}
		o.connSet = true
	}
}

// WithPublishQoS sets the QoS used by Publish. QoS 1 and 2 make Publish wait
// for the broker's acknowledgement; QoS 0 returns once the packet is written.
func WithPublishQoS(qos byte) Option {
	return func(o *options) {
		o.publishQoS = qos
	}
}

// WithSubscribeQoS sets the maximum QoS requested by Subscribe. The broker may
// grant a lower QoS than requested, which the adapter reports through the
// SUBACK reason code rather than silently accepting.
func WithSubscribeQoS(qos byte) Option {
	return func(o *options) {
		o.subscribeQoS = qos
	}
}

// WithRetain marks published messages as retained, so the broker keeps the
// last message per topic and delivers it to future subscribers.
func WithRetain(retain bool) Option {
	return func(o *options) {
		o.retain = retain
	}
}

// WithCleanStart discards any existing broker-side session on the first
// connection. It is the default because resuming a session that the
// application did not create is rarely what a fresh process wants; durable
// subscriptions opt in with WithCleanStart(false) plus WithSessionExpiry.
func WithCleanStart(clean bool) Option {
	return func(o *options) {
		o.cleanStart = clean
	}
}

// WithSessionExpiry sets the MQTT 5 session expiry interval in seconds. A zero
// value ends the session when the network connection closes, so queued QoS 1
// and 2 messages are discarded; a non-zero value keeps them for that long.
func WithSessionExpiry(seconds uint32) Option {
	return func(o *options) {
		o.sessionExpiry = seconds
	}
}

// WithKeepAlive sets the MQTT keepalive interval in seconds. It bounds how
// long a broken connection goes unnoticed before autopaho reconnects.
func WithKeepAlive(seconds uint16) Option {
	return func(o *options) {
		o.keepAlive = seconds
	}
}

// WithIDInUserProperty carries message.Message.ID in a user property instead
// of Correlation Data. Correlation Data is the default because it is the
// MQTT 5 field for correlating a message with its identity, but brokers and
// bridges that only forward user properties may need this instead.
func WithIDInUserProperty(enabled bool) Option {
	return func(o *options) {
		o.idInUserProp = enabled
	}
}

// WithAckOnError acknowledges a delivery whose handler returned an error.
//
// The default withholds the acknowledgement, which is the only redelivery
// signal MQTT 5 gives a subscriber: there is no negative acknowledgement, and
// a PUBACK reason code does not ask the broker to resend. An unacknowledged
// QoS 1 or 2 message is therefore redelivered when the session resumes, which
// requires WithCleanStart(false) and a non-zero WithSessionExpiry to survive a
// disconnect. Enabling this option discards failed messages instead, which
// suits handlers that treat their own errors as permanent.
func WithAckOnError(ack bool) Option {
	return func(o *options) {
		o.ackOnError = ack
	}
}

// WithErrorHandler observes asynchronous handler failures.
func WithErrorHandler(handler ErrorHandler) Option {
	return func(o *options) {
		o.errorHandler = handler
	}
}

// WithConnectWait bounds how long Publish and Subscribe wait for an
// established connection before failing. A non-positive value leaves the
// caller's context unchanged.
func WithConnectWait(timeout time.Duration) Option {
	return func(o *options) {
		o.connectWait = timeout
	}
}

// WithAckInterval sets how often buffered acknowledgements are flushed to the
// broker.
//
// MQTT requires acknowledgements to be sent in receipt order, so paho batches
// them behind a ticker rather than sending each one immediately. A message
// handled successfully but not yet flushed is redelivered if the connection
// drops first, so this bounds that window at the cost of more frequent wakeups.
func WithAckInterval(interval time.Duration) Option {
	return func(o *options) {
		o.ackInterval = interval
	}
}

// WithClientConfig adapts the autopaho configuration before the adapter
// connects. It is the escape hatch for TLS, authentication, and backoff, which
// are broker deployment concerns rather than parts of the message contract.
func WithClientConfig(fn func(*autopaho.ClientConfig)) Option {
	return func(o *options) {
		o.configure = fn
	}
}

// Client adapts one MQTT 5 connection to Forge message transport.
type Client struct {
	conn         connection
	ownsConn     bool
	publishQoS   byte
	subscribeQoS byte
	retain       bool
	idInUserProp bool
	errorHandler ErrorHandler
	connectWait  time.Duration

	// router dispatches an inbound PUBLISH to the subscriptions whose filter
	// matches it. The adapter owns it because one MQTT connection multiplexes
	// every subscription, unlike NATS where each subscription has its own
	// callback.
	router *router

	mu     sync.RWMutex
	closed bool
}

// New creates an MQTT 5 message adapter. Without WithConnectionManager, the
// returned client owns the connection and disconnects it from Close.
func New(ctx context.Context, opts ...Option) (*Client, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	cfg := options{
		cleanStart:  true,
		keepAlive:   30,
		connectWait: 10 * time.Second,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if err := validateQoS(cfg.publishQoS); err != nil {
		return nil, fmt.Errorf("mqtt: publish qos: %w", err)
	}
	if err := validateQoS(cfg.subscribeQoS); err != nil {
		return nil, fmt.Errorf("mqtt: subscribe qos: %w", err)
	}

	client := &Client{
		publishQoS:   cfg.publishQoS,
		subscribeQoS: cfg.subscribeQoS,
		retain:       cfg.retain,
		idInUserProp: cfg.idInUserProp,
		errorHandler: cfg.errorHandler,
		connectWait:  cfg.connectWait,
		router:       newRouter(cfg.ackOnError),
	}
	if cfg.connSet {
		if cfg.conn == nil {
			return nil, ErrNilConn
		}
		client.conn = cfg.conn
		return client, nil
	}
	if len(cfg.urls) == 0 {
		return nil, ErrEmptyURL
	}
	serverURLs, err := parseURLs(cfg.urls)
	if err != nil {
		return nil, err
	}
	cm, err := autopaho.NewConnection(ctx, client.clientConfig(serverURLs, &cfg))
	if err != nil {
		return nil, fmt.Errorf("mqtt: connect: %w", err)
	}
	client.conn = cm
	client.ownsConn = true
	return client, nil
}

// clientConfig builds the autopaho configuration. Resubscription is driven
// from OnConnectionUp so that a reconnect restores the application's
// subscriptions even when the broker reports no resumed session.
func (c *Client) clientConfig(serverURLs []*url.URL, cfg *options) autopaho.ClientConfig {
	acfg := autopaho.ClientConfig{
		ServerUrls:                    serverURLs,
		KeepAlive:                     cfg.keepAlive,
		CleanStartOnInitialConnection: cfg.cleanStart,
		SessionExpiryInterval:         cfg.sessionExpiry,
		OnConnectionUp: func(cm *autopaho.ConnectionManager, connack *paho.Connack) {
			c.router.resubscribe(cm, connack)
		},
		ClientConfig: paho.ClientConfig{
			ClientID: cfg.clientID,
			// Manual acknowledgement is what turns a handler error into a
			// withheld PUBACK; without it paho acks before the handler runs.
			EnableManualAcknowledgment: true,
			SendAcksInterval:           cfg.ackInterval,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){
				c.router.route,
			},
		},
	}
	if cfg.configure != nil {
		cfg.configure(&acfg)
	}
	return acfg
}

// Publish sends one MQTT 5 PUBLISH.
//
// At QoS 0 a successful return means the packet was written to the connection;
// the broker never confirms it. At QoS 1 and 2 it means the broker
// acknowledged with a success reason code. A cancellation or timeout after the
// packet is written is an ambiguous outcome at every QoS, so callers need an
// idempotency strategy before retrying.
func (c *Client) Publish(ctx context.Context, topic string, msg *message.Message) error {
	if ctx == nil {
		return ErrNilContext
	}
	if strings.TrimSpace(topic) == "" {
		return ErrEmptyTopic
	}
	if strings.ContainsAny(topic, "+#") {
		return fmt.Errorf("%w: %q", ErrWildcardPublish, topic)
	}
	if msg == nil {
		return ErrNilMessage
	}
	conn, err := c.connection()
	if err != nil {
		return err
	}
	ctx, cancel := c.waitContext(ctx)
	defer cancel()
	if err := conn.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("mqtt: publish %q: %w", topic, err)
	}
	resp, err := conn.Publish(ctx, c.toPublish(topic, msg))
	if err != nil {
		return fmt.Errorf("mqtt: publish %q: %w", topic, err)
	}
	// A QoS 0 publish has no response packet, so there is nothing to inspect.
	if resp != nil && resp.ReasonCode >= 0x80 {
		return fmt.Errorf("mqtt: publish %q: %w: reason 0x%02x", topic, ErrPublishRejected, resp.ReasonCode)
	}
	return nil
}

// Subscribe registers an MQTT 5 subscription. The topic may be a filter using
// the MQTT wildcards `+` and `#`; the handler receives the concrete topic that
// produced each delivery, not the filter it was registered with.
//
// The subscription lifetime is bound to ctx: cancellation unsubscribes and
// stops later deliveries.
func (c *Client) Subscribe(ctx context.Context, topic string, handler message.Handler) (message.Subscription, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(topic) == "" {
		return nil, ErrEmptyTopic
	}
	if handler == nil {
		return nil, ErrNilHandler
	}
	conn, err := c.connection()
	if err != nil {
		return nil, fmt.Errorf("mqtt: subscribe %q: %w", topic, err)
	}
	entry := c.router.add(topic, c.subscribeQoS, c.deliver(ctx, handler))

	waitCtx, cancel := c.waitContext(ctx)
	defer cancel()
	if err := conn.AwaitConnection(waitCtx); err != nil {
		c.router.remove(entry)
		return nil, fmt.Errorf("mqtt: subscribe %q: %w", topic, err)
	}
	suback, err := conn.Subscribe(waitCtx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: c.subscribeQoS}},
	})
	if err != nil {
		c.router.remove(entry)
		return nil, fmt.Errorf("mqtt: subscribe %q: %w", topic, err)
	}
	if err := subackError(topic, suback); err != nil {
		c.router.remove(entry)
		return nil, err
	}
	return newSubscription(ctx, c, entry, topic), nil
}

// deliver adapts a Forge handler to a routed MQTT delivery, giving the handler
// the subscription context so cancellation propagates into user code.
func (c *Client) deliver(subCtx context.Context, handler message.Handler) message.Handler {
	return func(_ context.Context, topic string, msg *message.Message) error {
		ctx := metadata.NewServerContext(subCtx, msg.Headers.Clone())
		err := handler(ctx, topic, msg)
		if err != nil && c.errorHandler != nil {
			c.errorHandler(ctx, topic, msg.Clone(), err)
		}
		return err
	}
}

// Close disconnects an adapter-owned connection. Managers supplied with
// WithConnectionManager are left connected.
//
// Acknowledgements are batched, so a message handled successfully within
// WithAckInterval of Close may not have been acknowledged yet and will be
// redelivered on the next session. Draining before shutdown is an application
// decision; the adapter does not delay Close to guess at one.
func (c *Client) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn, owns := c.conn, c.ownsConn
	c.mu.Unlock()

	c.router.reset()
	if !owns || conn == nil {
		return nil
	}
	if err := conn.Disconnect(ctx); err != nil {
		return fmt.Errorf("mqtt: disconnect: %w", err)
	}
	return nil
}

func (c *Client) connection() (connection, error) {
	if c == nil {
		return nil, ErrNilConn
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.conn == nil {
		return nil, ErrNilConn
	}
	return c.conn, nil
}

func (c *Client) waitContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.connectWait <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.connectWait)
}

func (c *Client) toPublish(topic string, msg *message.Message) *paho.Publish {
	props := &paho.PublishProperties{}
	for key, values := range msg.Headers {
		for _, value := range values {
			props.User.Add(key, value)
		}
	}
	if msg.ID != "" {
		if c.idInUserProp {
			props.User.Add(PropertyMessageID, msg.ID)
		} else {
			props.CorrelationData = []byte(msg.ID)
		}
	}
	if msg.Key != "" {
		props.User.Add(PropertyMessageKey, msg.Key)
	}
	return &paho.Publish{
		Topic:      topic,
		QoS:        c.publishQoS,
		Retain:     c.retain,
		Payload:    slices.Clone(msg.Body),
		Properties: props,
	}
}

// fromPublish rebuilds the portable message. Forge-owned properties are
// removed from the headers so a round trip does not duplicate them as both
// typed fields and headers.
func fromPublish(pub *paho.Publish) *message.Message {
	msg := message.New(pub.Payload)
	if pub.Properties == nil {
		return msg
	}
	msg.ID = string(pub.Properties.CorrelationData)
	for _, prop := range pub.Properties.User {
		switch prop.Key {
		case PropertyMessageKey:
			msg.Key = prop.Value
		case PropertyMessageID:
			msg.ID = prop.Value
		default:
			msg.Headers.Add(prop.Key, prop.Value)
		}
	}
	return msg
}

func validateQoS(qos byte) error {
	if qos > 2 {
		return fmt.Errorf("%w: %d", ErrInvalidQoS, qos)
	}
	return nil
}

func parseURLs(raw []string) ([]*url.URL, error) {
	urls := make([]*url.URL, 0, len(raw))
	for _, item := range raw {
		if strings.TrimSpace(item) == "" {
			return nil, ErrEmptyURL
		}
		parsed, err := url.Parse(item)
		if err != nil {
			return nil, fmt.Errorf("mqtt: parse url %q: %w", item, err)
		}
		urls = append(urls, parsed)
	}
	return urls, nil
}

// subackError reports a rejected subscription. Reason codes below 0x80 are the
// granted QoS, which may be lower than requested but is still a success.
func subackError(topic string, suback *paho.Suback) error {
	if suback == nil {
		return nil
	}
	for _, reason := range suback.Reasons {
		if reason >= 0x80 {
			return fmt.Errorf("mqtt: subscribe %q: %w: reason 0x%02x", topic, ErrSubscribeRejected, reason)
		}
	}
	return nil
}

type subscription struct {
	client  *Client
	entry   *routeEntry
	topic   string
	once    sync.Once
	done    chan struct{}
	stopped chan struct{}
	err     error
}

func newSubscription(ctx context.Context, client *Client, entry *routeEntry, topic string) *subscription {
	s := &subscription{
		client:  client,
		entry:   entry,
		topic:   topic,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go func() {
		defer close(s.stopped)
		select {
		case <-ctx.Done():
			// The subscription context is already gone, so unsubscribing needs
			// a context that is still live but bounded.
			unsubCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), client.connectWait)
			defer cancel()
			s.close(unsubCtx)
		case <-s.done:
		}
	}()
	return s
}

// Close stops delivery and unsubscribes. It is idempotent and safe to call
// concurrently with context cancellation.
func (s *subscription) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	err := s.close(ctx)
	<-s.stopped
	return err
}

func (s *subscription) close(ctx context.Context) error {
	s.once.Do(func() {
		// Routing stops first so no delivery can reach a handler after Close
		// returns, even if the UNSUBSCRIBE itself fails.
		s.client.router.remove(s.entry)
		close(s.done)
		conn, err := s.client.connection()
		if err != nil {
			// A closed client has already dropped the connection, so there is
			// no subscription left to remove at the broker.
			if errors.Is(err, ErrClosed) {
				return
			}
			s.err = err
			return
		}
		if _, err := conn.Unsubscribe(ctx, &paho.Unsubscribe{Topics: []string{s.topic}}); err != nil {
			s.err = fmt.Errorf("mqtt: unsubscribe %q: %w", s.topic, err)
		}
	})
	return s.err
}
