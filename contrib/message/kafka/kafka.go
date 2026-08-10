// Package kafka adapts Kafka topics to transport/message using franz-go.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

const (
	// HeaderMessageID carries message.Message.ID through a Kafka record header.
	// Kafka has no native message identity, so identity travels as data.
	HeaderMessageID = "forge-message-id"
)

var (
	// ErrNilContext reports an operation started without a context.
	ErrNilContext = errors.New("kafka: nil context")
	// ErrEmptyTopic reports an invalid Kafka topic.
	ErrEmptyTopic = errors.New("kafka: empty topic")
	// ErrNoSeedBrokers reports a client configured without broker addresses.
	ErrNoSeedBrokers = errors.New("kafka: no seed brokers")
	// ErrEmptyGroup reports a subscriber without a consumer group.
	ErrEmptyGroup = errors.New("kafka: empty consumer group")
	// ErrNilMessage reports an invalid message.
	ErrNilMessage = errors.New("kafka: nil message")
	// ErrNilHandler reports an invalid subscription handler.
	ErrNilHandler = errors.New("kafka: nil handler")
	// ErrNilClient reports an invalid client option.
	ErrNilClient = errors.New("kafka: nil client")
	// ErrClosed reports an adapter closed by its owner.
	ErrClosed = errors.New("kafka: publisher closed")
	// ErrWildcardSubscribe reports a subscription to a topic containing `*`,
	// `#`, or `>`. Kafka topics have no hierarchy and ConsumeTopics matches
	// literally, so such a subscription would join the group and never receive
	// a record. Those three characters are the multi- and single-level wildcards
	// of NATS, RabbitMQ, and MQTT, and none of them is legal in a Kafka topic
	// name, so their presence is a syntax borrowed from another broker rather
	// than an unusual topic. MQTT's `+` is deliberately not rejected: it is a
	// wildcard only in a filter, and screening it buys nothing that the three
	// illegal characters do not already cover.
	//
	// WithTopicRegex is the escape hatch, and lifts this check.
	ErrWildcardSubscribe = errors.New("kafka: wildcard in subscribe topic")
)

var (
	_ message.Publisher    = (*Publisher)(nil)
	_ message.Subscriber   = (*Subscriber)(nil)
	_ message.Subscription = (*subscription)(nil)
)

// FailureStage identifies where asynchronous consumption failed.
type FailureStage string

const (
	// StageFetch reports a broker or client error returned by a poll.
	StageFetch FailureStage = "fetch"
	// StageHandler reports a handler that returned an error.
	StageHandler FailureStage = "handler"
	// StageCommit reports an offset commit that did not reach the broker.
	StageCommit FailureStage = "commit"
)

// Failure describes an asynchronous consumer failure.
type Failure struct {
	Stage       FailureStage
	Destination string
	Partition   int32
	Offset      int64
	Message     *message.Message
	Err         error
}

// ErrorHandler observes failures that cannot be returned from Subscribe.
// The polling loop runs asynchronously, so a handler error has no caller to
// return to; the adapter reports it to the application instead of logging
// globally. Different subscriptions may call it concurrently.
type ErrorHandler func(context.Context, Failure)

// PublisherOption configures a Publisher.
type PublisherOption func(*publisherOptions)

type publisherOptions struct {
	seeds     []string
	client    *kgo.Client
	clientSet bool
	clientOpt []kgo.Opt
}

// WithPublisherSeedBrokers sets the bootstrap brokers used when the adapter
// owns the client.
func WithPublisherSeedBrokers(seeds ...string) PublisherOption {
	return func(o *publisherOptions) {
		o.seeds = append(o.seeds, seeds...)
	}
}

// WithPublisherClient uses an application-owned franz-go client. Publisher.Close
// will not close a client supplied this way.
func WithPublisherClient(client *kgo.Client) PublisherOption {
	return func(o *publisherOptions) {
		o.client = client
		o.clientSet = true
	}
}

// WithPublisherClientOptions appends franz-go options used to build an
// adapter-owned client. Applications reach compression, acks, partitioner, TLS,
// and SASL settings through here rather than through mirrored adapter options.
func WithPublisherClientOptions(opts ...kgo.Opt) PublisherOption {
	return func(o *publisherOptions) {
		o.clientOpt = append(o.clientOpt, opts...)
	}
}

// Publisher produces records to Kafka topics.
type Publisher struct {
	client     *kgo.Client
	ownsClient bool

	mu     sync.RWMutex
	closed bool
}

// NewPublisher creates a Kafka message publisher. Without WithPublisherClient
// the returned publisher owns the franz-go client and closes it from Close.
func NewPublisher(opts ...PublisherOption) (*Publisher, error) {
	var cfg publisherOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.clientSet {
		if cfg.client == nil {
			return nil, ErrNilClient
		}
		return &Publisher{client: cfg.client}, nil
	}
	if len(cfg.seeds) == 0 {
		return nil, ErrNoSeedBrokers
	}
	client, err := kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(cfg.seeds...)}, cfg.clientOpt...)...)
	if err != nil {
		return nil, fmt.Errorf("kafka: new producer client: %w", err)
	}
	return &Publisher{client: client, ownsClient: true}, nil
}

// Publish produces one record and waits for the broker response. A successful
// return therefore means the acks the client is configured for were received,
// not merely that the record was buffered locally. Cancellation after the record
// leaves the client is an ambiguous outcome: the broker may already have stored
// it, so retries need an idempotency strategy.
func (p *Publisher) Publish(ctx context.Context, topic string, msg *message.Message) error {
	if ctx == nil {
		return ErrNilContext
	}
	if strings.TrimSpace(topic) == "" {
		return ErrEmptyTopic
	}
	if msg == nil {
		return ErrNilMessage
	}
	client, err := p.connection()
	if err != nil {
		return err
	}
	results := client.ProduceSync(ctx, toRecord(topic, msg))
	if err := results.FirstErr(); err != nil {
		return fmt.Errorf("kafka: publish %q: %w", topic, err)
	}
	return nil
}

// Close closes an adapter-owned client. Application-owned clients supplied with
// WithPublisherClient are left open.
func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.ownsClient {
		p.client.Close()
	}
	return nil
}

func (p *Publisher) connection() (*kgo.Client, error) {
	if p == nil {
		return nil, ErrNilClient
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.client == nil {
		return nil, ErrNilClient
	}
	if p.closed {
		return nil, ErrClosed
	}
	return p.client, nil
}

// clientFactory builds the consuming client for one subscription. It exists so
// tests can supply a client bound to a fake broker without the adapter exposing
// franz-go construction as public API.
type clientFactory func(topic string, opts []kgo.Opt) (*kgo.Client, error)

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber)

// WithSubscriberSeedBrokers sets the bootstrap brokers for consuming clients.
func WithSubscriberSeedBrokers(seeds ...string) SubscriberOption {
	return func(s *Subscriber) {
		s.seeds = append(s.seeds, seeds...)
	}
}

// WithSubscriberClientOptions appends franz-go options used to build each
// consuming client. Session timeout, balancers, fetch sizing, TLS, and SASL are
// configured here. Group and topic assignment stay owned by the adapter.
func WithSubscriberClientOptions(opts ...kgo.Opt) SubscriberOption {
	return func(s *Subscriber) {
		s.clientOpt = append(s.clientOpt, opts...)
	}
}

// WithTopicRegex consumes topics by regular expression instead of by literal
// name, enabling franz-go's kgo.ConsumeRegex. Each destination is then compiled
// as a regular expression and matched against the topics the broker reports.
//
// This is the escape hatch from ErrWildcardSubscribe: it also suppresses the
// wildcard rejection, because `*` is meaningful in a regular expression. Both
// halves live in one option so the guard and the exemption cannot disagree —
// kgo.Opt is opaque, so an adapter cannot tell whether kgo.ConsumeRegex was
// passed through WithSubscriberClientOptions.
//
// Regex consumption is not a hierarchical wildcard. The pattern is evaluated
// against topic *names*, not against a message's key or a subject hierarchy,
// and a newly created topic only joins the subscription after the client's next
// metadata refresh.
func WithTopicRegex() SubscriberOption {
	return func(s *Subscriber) {
		s.topicRegex = true
	}
}

// WithErrorHandler observes fetch, handler, and commit failures.
func WithErrorHandler(handler ErrorHandler) SubscriberOption {
	return func(s *Subscriber) {
		s.onError = handler
	}
}

// WithMaxPollRecords bounds how many records one poll returns. Smaller batches
// shorten the window in which a slow handler can exceed the rebalance timeout.
// A non-positive value polls whole fetches.
func WithMaxPollRecords(n int) SubscriberOption {
	return func(s *Subscriber) {
		s.maxPollRecords = n
	}
}

// Subscriber consumes Kafka topics through a consumer group.
//
// Every Subscribe creates its own franz-go client. message.Server registers one
// destination at a time and owns each subscription independently, so a shared
// client would make one destination's shutdown revoke another's partitions.
type Subscriber struct {
	group          string
	seeds          []string
	clientOpt      []kgo.Opt
	onError        ErrorHandler
	maxPollRecords int
	topicRegex     bool
	newClient      clientFactory
}

// NewSubscriber creates a consumer-group subscriber. The group is adapter-wide:
// every destination is consumed by the same group, so scaling replicas divides
// partitions between them rather than duplicating delivery.
func NewSubscriber(group string, opts ...SubscriberOption) (*Subscriber, error) {
	if strings.TrimSpace(group) == "" {
		return nil, ErrEmptyGroup
	}
	s := &Subscriber{group: group}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if s.newClient == nil {
		if len(s.seeds) == 0 {
			return nil, ErrNoSeedBrokers
		}
		s.newClient = s.defaultClient
	}
	return s, nil
}

func (s *Subscriber) defaultClient(_ string, opts []kgo.Opt) (*kgo.Client, error) {
	return kgo.NewClient(append([]kgo.Opt{kgo.SeedBrokers(s.seeds...)}, opts...)...)
}

// Subscribe joins the consumer group for one topic and delivers records to
// handler until ctx is canceled or the subscription is closed.
//
// Delivery is at-least-once. Automatic committing is disabled and offsets are
// committed only after the handler returns nil, so a crash between processing
// and commit redelivers rather than skips. A handler error stops the batch: the
// records processed before it stay committed, the failing record and everything
// after it in that poll are left uncommitted, and consumption continues from
// the failing offset after the next rebalance or restart. The adapter does not
// retry in place, because an in-process retry loop would silently stall the
// partition; dead-lettering and backoff belong to application policy.
//
// Records are processed one at a time in fetch order. Kafka only orders records
// within a partition, so this preserves per-partition order without assuming
// order across partitions.
//
// The topic is matched literally. A topic containing `*`, `#`, or `>` is
// rejected with ErrWildcardSubscribe rather than accepted into a subscription
// that can never deliver: those characters are the wildcard syntax of MQTT,
// RabbitMQ, and NATS, and a Kafka topic named after one of their patterns
// almost never exists. Consuming by pattern is still possible, but must be
// asked for with WithTopicRegex, which matches the topic as a regular
// expression against the broker's topic list and lifts this rejection.
func (s *Subscriber) Subscribe(ctx context.Context, topic string, handler message.Handler) (message.Subscription, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(topic) == "" {
		return nil, ErrEmptyTopic
	}
	if handler == nil {
		return nil, ErrNilHandler
	}
	if s == nil || s.newClient == nil {
		return nil, ErrNilClient
	}
	if !s.topicRegex && strings.ContainsAny(topic, "*#>") {
		return nil, fmt.Errorf("%w: %q", ErrWildcardSubscribe, topic)
	}
	// BlockRebalanceOnPoll keeps ownership of the polled partitions until the
	// batch is committed, so commits cannot land on partitions already
	// reassigned to another member.
	opts := slices.Concat(s.clientOpt, []kgo.Opt{
		kgo.ConsumerGroup(s.group),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
	})
	if s.topicRegex {
		opts = append(opts, kgo.ConsumeRegex())
	}
	client, err := s.newClient(topic, opts)
	if err != nil {
		return nil, fmt.Errorf("kafka: subscribe %q: %w", topic, err)
	}
	sub := &subscription{client: client, done: make(chan struct{})}
	go func() {
		defer close(sub.done)
		s.consume(ctx, client, topic, handler)
	}()
	return sub, nil
}

func (s *Subscriber) consume(ctx context.Context, client *kgo.Client, topic string, handler message.Handler) {
	// A poll that returned records leaves rebalances blocked until they are
	// allowed again, and Client.Close waits for that. Releasing on the way out
	// keeps a canceled loop from deadlocking the subsequent close.
	defer client.AllowRebalance()
	for {
		fetches := s.poll(ctx, client)
		if fetches.IsClientClosed() || ctx.Err() != nil {
			return
		}
		fetches.EachError(func(fetchTopic string, partition int32, err error) {
			s.report(ctx, Failure{Stage: StageFetch, Destination: fetchTopic, Partition: partition, Err: err})
		})
		s.process(ctx, client, topic, handler, fetches)
		// AllowRebalance must run after commits, and on every iteration:
		// BlockRebalanceOnPoll leaves rebalances blocked until it is called.
		client.AllowRebalance()
	}
}

func (s *Subscriber) poll(ctx context.Context, client *kgo.Client) kgo.Fetches {
	if s.maxPollRecords > 0 {
		return client.PollRecords(ctx, s.maxPollRecords)
	}
	return client.PollFetches(ctx)
}

// process runs handlers in fetch order and commits the longest prefix of
// records that succeeded. Committing once per batch rather than once per record
// keeps offset-commit load off the broker.
//
// The prefix is taken across the whole batch, not per partition, so a failure
// on one partition also abandons the not-yet-run records of others in the same
// poll. Those are redelivered rather than lost, which is the conservative
// reading of at-least-once; tracking a separate failure point per partition
// would commit further but is not needed for correctness.
func (s *Subscriber) process(ctx context.Context, client *kgo.Client, topic string, handler message.Handler, fetches kgo.Fetches) {
	var processed []*kgo.Record
	for iter := fetches.RecordIter(); !iter.Done(); {
		record := iter.Next()
		if ctx.Err() != nil {
			break
		}
		msg := fromRecord(record)
		handlerCtx := metadata.NewServerContext(ctx, msg.Headers.Clone())
		if err := handler(handlerCtx, record.Topic, msg); err != nil {
			s.report(handlerCtx, Failure{
				Stage:       StageHandler,
				Destination: record.Topic,
				Partition:   record.Partition,
				Offset:      record.Offset,
				Message:     msg.Clone(),
				Err:         err,
			})
			break
		}
		processed = append(processed, record)
	}
	if len(processed) == 0 {
		return
	}
	// Commit with a context detached from the subscription lifetime: a canceled
	// subscription must still checkpoint the work its handlers already did,
	// otherwise shutdown guarantees redelivery of everything in flight.
	commitCtx := context.WithoutCancel(ctx)
	if err := client.CommitRecords(commitCtx, processed...); err != nil {
		last := processed[len(processed)-1]
		s.report(ctx, Failure{
			Stage:       StageCommit,
			Destination: topic,
			Partition:   last.Partition,
			Offset:      last.Offset,
			Err:         err,
		})
	}
}

func (s *Subscriber) report(ctx context.Context, failure Failure) {
	if s.onError != nil {
		s.onError(ctx, failure)
	}
}

type subscription struct {
	client *kgo.Client
	once   sync.Once
	done   chan struct{}
}

// Close leaves the consumer group and waits for the polling loop to finish, so
// no handler runs after it returns. A ctx deadline bounds the wait; the client
// is closed either way.
func (s *subscription) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	s.once.Do(s.client.Close)
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func toRecord(topic string, msg *message.Message) *kgo.Record {
	record := &kgo.Record{
		Topic: topic,
		Value: slices.Clone(msg.Body),
	}
	if msg.Key != "" {
		record.Key = []byte(msg.Key)
	}
	for key, values := range msg.Headers {
		for _, value := range values {
			record.Headers = append(record.Headers, kgo.RecordHeader{Key: key, Value: []byte(value)})
		}
	}
	if msg.ID != "" {
		record.Headers = append(record.Headers, kgo.RecordHeader{Key: HeaderMessageID, Value: []byte(msg.ID)})
	}
	return record
}

func fromRecord(record *kgo.Record) *message.Message {
	msg := message.New(record.Value)
	msg.Key = string(record.Key)
	msg.Headers = metadata.Metadata{}
	for _, header := range record.Headers {
		msg.Headers.Add(header.Key, string(header.Value))
	}
	msg.ID = msg.Headers.Get(HeaderMessageID)
	return msg
}
