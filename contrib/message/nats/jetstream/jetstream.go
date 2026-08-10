// Package jetstream adapts durable NATS JetStream messaging to transport/message.
package jetstream

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
	natsjs "github.com/nats-io/nats.go/jetstream"

	corenats "github.com/sylphylabs/forge/contrib/message/nats"
	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

var (
	// ErrNilContext reports an operation started without a context.
	ErrNilContext = errors.New("jetstream: nil context")
	// ErrNilJetStream reports a missing JetStream client.
	ErrNilJetStream = errors.New("jetstream: nil client")
	// ErrEmptyDestination reports an invalid message destination.
	ErrEmptyDestination = errors.New("jetstream: empty destination")
	// ErrNilMessage reports an invalid message.
	ErrNilMessage = errors.New("jetstream: nil message")
	// ErrNilHandler reports an invalid subscription handler.
	ErrNilHandler = errors.New("jetstream: nil handler")
	// ErrBindingNotFound reports a destination with no configured consumer.
	ErrBindingNotFound = errors.New("jetstream: binding not found")
	// ErrInvalidBinding reports an incomplete stream/consumer binding.
	ErrInvalidBinding = errors.New("jetstream: invalid binding")
	// ErrInvalidAckMode reports an unsupported acknowledgement mode.
	ErrInvalidAckMode = errors.New("jetstream: invalid ack mode")
	// ErrInvalidDisposition reports an unsupported handler error disposition.
	ErrInvalidDisposition = errors.New("jetstream: invalid error disposition")
)

// Binding maps one message.Server destination to an existing durable consumer.
// Infrastructure provisioning remains outside the adapter.
type Binding struct {
	Stream   string
	Consumer string
}

// AckMode controls how successful handler completion is acknowledged.
type AckMode uint8

const (
	// AckConfirmed waits for the server to confirm the acknowledgement.
	AckConfirmed AckMode = iota
	// AckAsync sends the acknowledgement without waiting for confirmation.
	AckAsync
)

// ErrorDisposition selects the JetStream outcome for a handler error.
type ErrorDisposition uint8

const (
	// Retry asks JetStream to redeliver the message after the configured delay.
	Retry ErrorDisposition = iota
	// Terminate stops redelivery of the message.
	Terminate
)

// ErrorClassifier classifies a handler error without exposing JetStream SDK
// message types to application code.
type ErrorClassifier func(context.Context, *message.Message, error) ErrorDisposition

// FailureStage identifies where asynchronous processing failed.
type FailureStage string

const (
	StageConsume     FailureStage = "consume"
	StageHandler     FailureStage = "handler"
	StageAcknowledge FailureStage = "acknowledge"
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

// Publisher publishes messages and waits for JetStream PubAck responses.
type Publisher struct {
	js      natsjs.JetStream
	options []natsjs.PublishOpt
}

var _ message.Publisher = (*Publisher)(nil)

// PublisherOption configures a Publisher.
type PublisherOption func(*Publisher)

// WithPublishOptions appends JetStream publish options to every publish.
func WithPublishOptions(opts ...natsjs.PublishOpt) PublisherOption {
	return func(p *Publisher) {
		p.options = append(p.options, opts...)
	}
}

// NewPublisher creates a durable message publisher.
func NewPublisher(js natsjs.JetStream, opts ...PublisherOption) (*Publisher, error) {
	if js == nil {
		return nil, ErrNilJetStream
	}
	p := &Publisher{js: js}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p, nil
}

// Publish implements message.Publisher and waits for a JetStream PubAck.
func (p *Publisher) Publish(ctx context.Context, destination string, msg *message.Message) error {
	_, err := p.PublishAck(ctx, destination, msg)
	return err
}

// PublishAck publishes one message and returns JetStream acknowledgement
// details, including duplicate detection and stream sequence.
func (p *Publisher) PublishAck(ctx context.Context, destination string, msg *message.Message, opts ...natsjs.PublishOpt) (*natsjs.PubAck, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(destination) == "" {
		return nil, ErrEmptyDestination
	}
	if msg == nil {
		return nil, ErrNilMessage
	}
	if p == nil || p.js == nil {
		return nil, ErrNilJetStream
	}
	publishOpts := slices.Concat(p.options, opts)
	if msg.ID != "" {
		publishOpts = append(publishOpts, natsjs.WithMsgID(msg.ID))
	}
	ack, err := p.js.PublishMsg(ctx, toNATSMsg(destination, msg), publishOpts...)
	if err != nil {
		return nil, fmt.Errorf("jetstream: publish %q: %w", destination, err)
	}
	if ack == nil {
		return nil, fmt.Errorf("jetstream: publish %q: nil acknowledgement", destination)
	}
	return ack, nil
}

// SubscriberOption configures a Subscriber.
type SubscriberOption func(*Subscriber) error

// WithAckMode selects confirmed or asynchronous acknowledgement on success.
func WithAckMode(mode AckMode) SubscriberOption {
	return func(s *Subscriber) error {
		if mode != AckConfirmed && mode != AckAsync {
			return ErrInvalidAckMode
		}
		s.ackMode = mode
		return nil
	}
}

// WithRetryDelay sets the delay used when a handler error is classified Retry.
func WithRetryDelay(delay time.Duration) SubscriberOption {
	return func(s *Subscriber) error {
		s.retryDelay = delay
		return nil
	}
}

// WithErrorClassifier classifies handler failures as Retry or Terminate.
func WithErrorClassifier(classifier ErrorClassifier) SubscriberOption {
	return func(s *Subscriber) error {
		if classifier != nil {
			s.classify = classifier
		}
		return nil
	}
}

// WithErrorHandler observes handler, acknowledgement, and consume failures.
func WithErrorHandler(handler ErrorHandler) SubscriberOption {
	return func(s *Subscriber) error {
		s.onError = handler
		return nil
	}
}

// WithConsumeOptions appends options passed to each pull consumer Consume call.
func WithConsumeOptions(opts ...natsjs.PullConsumeOpt) SubscriberOption {
	return func(s *Subscriber) error {
		s.consumeOptions = append(s.consumeOptions, opts...)
		return nil
	}
}

// Subscriber binds message.Server destinations to existing JetStream pull
// consumers. It never creates or updates streams or consumers.
type Subscriber struct {
	js             natsjs.JetStream
	bindings       map[string]Binding
	ackMode        AckMode
	retryDelay     time.Duration
	classify       ErrorClassifier
	onError        ErrorHandler
	consumeOptions []natsjs.PullConsumeOpt
}

var _ message.Subscriber = (*Subscriber)(nil)

// NewSubscriber creates a durable subscriber for existing consumers.
func NewSubscriber(js natsjs.JetStream, bindings map[string]Binding, opts ...SubscriberOption) (*Subscriber, error) {
	if js == nil {
		return nil, ErrNilJetStream
	}
	cloned := make(map[string]Binding, len(bindings))
	for destination, binding := range bindings {
		if strings.TrimSpace(destination) == "" || strings.TrimSpace(binding.Stream) == "" || strings.TrimSpace(binding.Consumer) == "" {
			return nil, fmt.Errorf("%w for %q", ErrInvalidBinding, destination)
		}
		cloned[destination] = binding
	}
	s := &Subscriber{
		js:         js,
		bindings:   cloned,
		ackMode:    AckConfirmed,
		retryDelay: time.Second,
		classify: func(context.Context, *message.Message, error) ErrorDisposition {
			return Retry
		},
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(s); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Subscribe binds a handler to the existing consumer configured for destination.
func (s *Subscriber) Subscribe(ctx context.Context, destination string, handler message.Handler) (message.Subscription, error) {
	if ctx == nil {
		return nil, ErrNilContext
	}
	if strings.TrimSpace(destination) == "" {
		return nil, ErrEmptyDestination
	}
	if handler == nil {
		return nil, ErrNilHandler
	}
	if s == nil || s.js == nil {
		return nil, ErrNilJetStream
	}
	binding, ok := s.bindings[destination]
	if !ok {
		return nil, fmt.Errorf("%w for %q", ErrBindingNotFound, destination)
	}
	consumer, err := s.js.Consumer(ctx, binding.Stream, binding.Consumer)
	if err != nil {
		return nil, fmt.Errorf("jetstream: bind %q to %s/%s: %w", destination, binding.Stream, binding.Consumer, err)
	}
	consumeOptions := slices.Clone(s.consumeOptions)
	consumeOptions = append(consumeOptions, natsjs.ConsumeErrHandler(func(_ natsjs.ConsumeContext, err error) {
		s.report(ctx, Failure{Stage: StageConsume, Destination: destination, Err: err})
	}))
	consume, err := consumer.Consume(func(jsMsg natsjs.Msg) {
		s.handle(ctx, destination, handler, jsMsg)
	}, consumeOptions...)
	if err != nil {
		return nil, fmt.Errorf("jetstream: consume %q from %s/%s: %w", destination, binding.Stream, binding.Consumer, err)
	}
	return newSubscription(ctx, consume), nil
}

func (s *Subscriber) handle(ctx context.Context, destination string, handler message.Handler, jsMsg natsjs.Msg) {
	msg := fromJetStreamMsg(jsMsg)
	handlerCtx := metadata.NewServerContext(ctx, msg.Headers.Clone())
	handlerErr := handler(handlerCtx, jsMsg.Subject(), msg)
	if handlerErr == nil {
		var ackErr error
		if s.ackMode == AckConfirmed {
			ackErr = jsMsg.DoubleAck(handlerCtx)
		} else {
			ackErr = jsMsg.Ack()
		}
		if ackErr != nil {
			s.report(handlerCtx, Failure{
				Stage:       StageAcknowledge,
				Destination: destination,
				Message:     msg.Clone(),
				Err:         ackErr,
			})
		}
		return
	}

	disposition := s.classify(handlerCtx, msg, handlerErr)
	var dispositionErr error
	switch disposition {
	case Retry:
		if s.retryDelay > 0 {
			dispositionErr = jsMsg.NakWithDelay(s.retryDelay)
		} else {
			dispositionErr = jsMsg.Nak()
		}
	case Terminate:
		dispositionErr = jsMsg.Term()
	default:
		dispositionErr = ErrInvalidDisposition
		if termErr := jsMsg.Term(); termErr != nil {
			dispositionErr = errors.Join(dispositionErr, termErr)
		}
	}
	s.report(handlerCtx, Failure{
		Stage:       StageHandler,
		Destination: destination,
		Message:     msg.Clone(),
		Err:         errors.Join(handlerErr, dispositionErr),
	})
}

func (s *Subscriber) report(ctx context.Context, failure Failure) {
	if s.onError != nil {
		s.onError(ctx, failure)
	}
}

type subscription struct {
	consume natsjs.ConsumeContext
	once    sync.Once
}

var _ message.Subscription = (*subscription)(nil)

func newSubscription(ctx context.Context, consume natsjs.ConsumeContext) *subscription {
	s := &subscription{consume: consume}
	go func() {
		select {
		case <-ctx.Done():
			consume.Stop()
		case <-consume.Closed():
		}
	}()
	return s
}

func (s *subscription) Close(ctx context.Context) error {
	if ctx == nil {
		return ErrNilContext
	}
	s.once.Do(s.consume.Drain)
	select {
	case <-s.consume.Closed():
		return nil
	case <-ctx.Done():
		s.consume.Stop()
		return ctx.Err()
	}
}

func toNATSMsg(destination string, msg *message.Message) *natsgo.Msg {
	natsMsg := natsgo.NewMsg(destination)
	natsMsg.Data = slices.Clone(msg.Body)
	for key, values := range msg.Headers {
		for _, value := range values {
			natsMsg.Header.Add(key, value)
		}
	}
	if msg.ID != "" {
		natsMsg.Header.Set(corenats.HeaderMessageID, msg.ID)
	}
	if msg.Key != "" {
		natsMsg.Header.Set(corenats.HeaderMessageKey, msg.Key)
	}
	return natsMsg
}

func fromJetStreamMsg(jsMsg natsjs.Msg) *message.Message {
	msg := message.New(jsMsg.Data())
	msg.ID = jsMsg.Headers().Get(corenats.HeaderMessageID)
	if msg.ID == "" {
		msg.ID = jsMsg.Headers().Get(natsjs.MsgIDHeader)
	}
	msg.Key = jsMsg.Headers().Get(corenats.HeaderMessageKey)
	msg.Headers = metadata.Metadata{}
	for key, values := range jsMsg.Headers() {
		for _, value := range values {
			msg.Headers.Add(key, value)
		}
	}
	return msg
}
