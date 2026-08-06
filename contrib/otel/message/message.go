// Package message instruments asynchronous message publishing and processing
// with OpenTelemetry without adding telemetry dependencies to the core
// transport/message package.
package message

import (
	"context"
	"errors"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/sylphylabs/forge/metadata"
	transportmessage "github.com/sylphylabs/forge/transport/message"
)

const instrumentationName = "github.com/sylphylabs/forge/contrib/otel/message"

// ErrNilPublisher reports an instrumentation wrapper without a publisher.
var ErrNilPublisher = errors.New("otel/message: nil publisher")

// Option configures message tracing. Defaults are local and do not read the
// process-wide OpenTelemetry provider or propagator.
type Option func(*options)

type options struct {
	tracerProvider trace.TracerProvider
	propagator     propagation.TextMapPropagator
	system         string
}

// WithTracerProvider sets the provider used to create message spans.
func WithTracerProvider(provider trace.TracerProvider) Option {
	return func(opts *options) {
		if provider != nil {
			opts.tracerProvider = provider
		}
	}
}

// WithPropagator sets the propagator used with Message.Headers.
func WithPropagator(propagator propagation.TextMapPropagator) Option {
	return func(opts *options) {
		if propagator != nil {
			opts.propagator = propagator
		}
	}
}

// WithSystem sets the messaging system semantic attribute, such as "nats",
// "kafka", or "rabbitmq". The instrumentation does not infer it from the
// wrapped publisher or subscriber.
func WithSystem(system string) Option {
	return func(opts *options) {
		opts.system = strings.TrimSpace(system)
	}
}

func newOptions(opts []Option) options {
	configured := options{
		tracerProvider: noop.NewTracerProvider(),
		propagator:     propagation.TraceContext{},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&configured)
		}
	}
	return configured
}

// Publisher traces calls to a wrapped protocol-neutral publisher.
type Publisher struct {
	next       transportmessage.Publisher
	tracer     trace.Tracer
	propagator propagation.TextMapPropagator
	system     string
}

var _ transportmessage.Publisher = (*Publisher)(nil)

// NewPublisher decorates next with producer spans and trace-context injection.
// Publish injects into a cloned message and never mutates the caller's message.
func NewPublisher(next transportmessage.Publisher, opts ...Option) *Publisher {
	configured := newOptions(opts)
	return &Publisher{
		next:       next,
		tracer:     configured.tracerProvider.Tracer(instrumentationName, trace.WithSchemaURL(semconv.SchemaURL)),
		propagator: configured.propagator,
		system:     configured.system,
	}
}

// Publish creates a producer span and injects its context into a message clone.
func (p *Publisher) Publish(ctx context.Context, destination string, msg *transportmessage.Message) (err error) {
	if p == nil || p.next == nil {
		return ErrNilPublisher
	}
	if ctx == nil {
		return p.next.Publish(ctx, destination, msg)
	}

	ctx, span := p.tracer.Start(
		ctx,
		spanName(destination, "send"),
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(spanAttributes(p.system, destination, "send", msg)...),
	)
	defer func() { finishSpan(span, err) }()

	cloned := msg.Clone()
	if cloned != nil {
		if cloned.Headers == nil {
			cloned.Headers = make(metadata.Metadata)
		}
		p.propagator.Inject(ctx, metadataCarrier(cloned.Headers))
	}
	return p.next.Publish(ctx, destination, cloned)
}

// Consumer returns middleware that extracts a parent context from message
// headers and creates a consumer span around message processing.
func Consumer(opts ...Option) transportmessage.Middleware {
	configured := newOptions(opts)
	tracer := configured.tracerProvider.Tracer(instrumentationName, trace.WithSchemaURL(semconv.SchemaURL))
	return func(next transportmessage.Handler) transportmessage.Handler {
		return func(ctx context.Context, destination string, msg *transportmessage.Message) (err error) {
			if ctx == nil {
				return next(ctx, destination, msg)
			}
			if msg != nil {
				ctx = configured.propagator.Extract(ctx, metadataCarrier(msg.Headers))
			}
			ctx, span := tracer.Start(
				ctx,
				spanName(destination, "process"),
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(spanAttributes(configured.system, destination, "process", msg)...),
			)
			defer func() { finishSpan(span, err) }()
			return next(ctx, destination, msg)
		}
	}
}

func spanAttributes(system, destination, operation string, msg *transportmessage.Message) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.MessagingOperationName(operation),
	}
	if system != "" {
		attrs = append(attrs, semconv.MessagingSystemKey.String(system))
	}
	if destination != "" {
		attrs = append(attrs, semconv.MessagingDestinationName(destination))
	}
	if operation == "send" {
		attrs = append(attrs, semconv.MessagingOperationTypeSend)
	} else {
		attrs = append(attrs, semconv.MessagingOperationTypeProcess)
	}
	if msg != nil && msg.ID != "" {
		attrs = append(attrs, semconv.MessagingMessageID(msg.ID))
	}
	return attrs
}

func spanName(destination, operation string) string {
	if destination == "" {
		return operation
	}
	return destination + " " + operation
}

func finishSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

type metadataCarrier metadata.Metadata

func (carrier metadataCarrier) Get(key string) string {
	return metadata.Metadata(carrier).Get(key)
}

func (carrier metadataCarrier) Set(key, value string) {
	metadata.Metadata(carrier).Set(key, value)
}

func (carrier metadataCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier))
	for key := range carrier {
		keys = append(keys, key)
	}
	return keys
}
