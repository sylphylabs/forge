package message

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	transportmessage "github.com/openkratos/kratos/transport/message"
)

type recordingPublisher struct {
	message *transportmessage.Message
	ctx     context.Context
	err     error
}

func (p *recordingPublisher) Publish(ctx context.Context, _ string, msg *transportmessage.Message) error {
	p.ctx = ctx
	p.message = msg
	return p.err
}

func newTestProvider() (*trace.TracerProvider, *tracetest.SpanRecorder) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	return provider, recorder
}

func TestPublisherInjectsIntoClone(t *testing.T) {
	provider, recorder := newTestProvider()
	parentCtx, parent := provider.Tracer("test").Start(context.Background(), "request")
	original := transportmessage.New([]byte("body"))
	original.ID = "message-1"
	original.AddHeader("x-existing", "one")
	original.AddHeader("x-existing", "two")

	receiver := new(recordingPublisher)
	err := NewPublisher(
		receiver,
		WithTracerProvider(provider),
		WithPropagator(propagation.TraceContext{}),
		WithSystem("nats"),
	).Publish(parentCtx, "orders.created", original)
	parent.End()
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if receiver.message == original {
		t.Fatal("Publish() passed the original message")
	}
	if got := original.Headers.Get("traceparent"); got != "" {
		t.Fatalf("Publish() mutated original traceparent = %q", got)
	}
	if got := receiver.message.Headers.Values("x-existing"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("Publish() changed multi-value headers = %#v", got)
	}
	if receiver.message.Headers.Get("traceparent") == "" {
		t.Fatal("Publish() did not inject traceparent")
	}

	spans := recorder.Ended()
	producer := findSpan(t, spans, "orders.created send")
	if producer.SpanKind() != oteltrace.SpanKindProducer {
		t.Fatalf("SpanKind = %v, want producer", producer.SpanKind())
	}
	if producer.Status().Code != 0 {
		t.Fatalf("successful producer status = %v, want unset", producer.Status().Code)
	}
	assertAttribute(t, producer, semconv.MessagingSystemKey, "nats")
	assertAttribute(t, producer, semconv.MessagingDestinationNameKey, "orders.created")
	assertAttribute(t, producer, semconv.MessagingMessageIDKey, "message-1")
}

func TestConsumerExtractsParentAndRecordsError(t *testing.T) {
	provider, recorder := newTestProvider()
	producerCtx, producer := provider.Tracer("test").Start(context.Background(), "producer")
	msg := transportmessage.New([]byte("body"))
	propagator := propagation.TraceContext{}
	propagator.Inject(producerCtx, metadataCarrier(msg.Headers))
	producer.End()

	wantErr := errors.New("handler failed")
	var observed oteltrace.SpanContext
	handler := Consumer(
		WithTracerProvider(provider),
		WithPropagator(propagator),
		WithSystem("nats"),
	)(func(ctx context.Context, _ string, _ *transportmessage.Message) error {
		observed = oteltrace.SpanContextFromContext(ctx)
		return wantErr
	})
	if err := handler(context.Background(), "orders.created", msg); !errors.Is(err, wantErr) {
		t.Fatalf("handler() error = %v, want %v", err, wantErr)
	}

	consumer := findSpan(t, recorder.Ended(), "orders.created process")
	if consumer.SpanKind() != oteltrace.SpanKindConsumer {
		t.Fatalf("SpanKind = %v, want consumer", consumer.SpanKind())
	}
	if observed.TraceID() != producer.SpanContext().TraceID() {
		t.Fatalf("consumer trace ID = %s, want %s", observed.TraceID(), producer.SpanContext().TraceID())
	}
	if consumer.Parent().SpanID() != producer.SpanContext().SpanID() {
		t.Fatalf("consumer parent span ID = %s, want %s", consumer.Parent().SpanID(), producer.SpanContext().SpanID())
	}
	if consumer.Status().Code != codes.Error {
		t.Fatalf("error consumer status = %v, want error", consumer.Status().Code)
	}
	if len(consumer.Events()) == 0 {
		t.Fatal("error consumer span has no recorded error event")
	}
}

func TestCustomProviderAndPropagator(t *testing.T) {
	provider, recorder := newTestProvider()
	propagator := customPropagator{key: key}
	receiver := new(recordingPublisher)
	ctx := context.WithValue(context.Background(), contextCarrier{}, "custom-value")
	if err := NewPublisher(receiver, WithTracerProvider(provider), WithPropagator(propagator)).Publish(ctx, "custom", transportmessage.New(nil)); err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if got := receiver.message.Headers.Get("x-custom-trace"); got != "custom-value" {
		t.Fatalf("custom propagated value = %q", got)
	}
	if got := len(recorder.Ended()); got != 1 {
		t.Fatalf("custom provider ended spans = %d, want 1", got)
	}

	wire := transportmessage.New(nil)
	wire.Headers.Set("x-custom-trace", "received-value")
	var extracted any
	handler := Consumer(
		WithTracerProvider(provider),
		WithPropagator(propagator),
	)(func(ctx context.Context, _ string, _ *transportmessage.Message) error {
		extracted = ctx.Value(key)
		return nil
	})
	if err := handler(context.Background(), "custom", wire); err != nil {
		t.Fatalf("Consumer() error = %v", err)
	}
	if extracted != "received-value" {
		t.Fatalf("custom extracted value = %v", extracted)
	}
}

func TestNilMessageIsTransparent(t *testing.T) {
	receiver := new(recordingPublisher)
	publisher := NewPublisher(receiver)
	if err := publisher.Publish(context.Background(), "empty", nil); err != nil {
		t.Fatalf("Publish(nil) error = %v", err)
	}
	if receiver.message != nil {
		t.Fatal("Publish(nil) did not preserve nil message")
	}

	called := false
	handler := Consumer()(func(_ context.Context, _ string, msg *transportmessage.Message) error {
		called = true
		if msg != nil {
			t.Fatalf("handler message = %#v, want nil", msg)
		}
		return nil
	})
	if err := handler(context.Background(), "empty", nil); err != nil {
		t.Fatalf("Consumer()(nil) error = %v", err)
	}
	if !called {
		t.Fatal("Consumer()(nil) did not call handler")
	}
}

func TestPublisherErrorStatus(t *testing.T) {
	wantErr := errors.New("publish failed")
	provider, recorder := newTestProvider()
	if err := NewPublisher(&recordingPublisher{err: wantErr}, WithTracerProvider(provider)).Publish(context.Background(), "orders", transportmessage.New(nil)); !errors.Is(err, wantErr) {
		t.Fatalf("Publish() error = %v, want %v", err, wantErr)
	}
	span := findSpan(t, recorder.Ended(), "orders send")
	if span.Status().Code != codes.Error {
		t.Fatalf("error publisher status = %v, want error", span.Status().Code)
	}
}

func findSpan(t *testing.T, spans []trace.ReadOnlySpan, name string) trace.ReadOnlySpan {
	t.Helper()
	for _, span := range spans {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("span %q not found", name)
	return nil
}

func assertAttribute(t *testing.T, span trace.ReadOnlySpan, key attribute.Key, want string) {
	t.Helper()
	for _, attr := range span.Attributes() {
		if attr.Key == key && attr.Value.AsString() == want {
			return
		}
	}
	t.Fatalf("span %q missing %s=%q", span.Name(), key, want)
}

var key = struct{}{}

type contextCarrier struct{}

type customPropagator struct{ key any }

func (p customPropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	if value, ok := ctx.Value(contextCarrier{}).(string); ok {
		carrier.Set("x-custom-trace", value)
	}
}

func (p customPropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return context.WithValue(ctx, p.key, carrier.Get("x-custom-trace"))
}

func (customPropagator) Fields() []string { return []string{"x-custom-trace"} }
