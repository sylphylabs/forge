package message_test

// The example in this file mirrors the "Asynchronous messages" snippet in
// docs/agent/observability.md so that the guide cannot drift from the API
// without breaking the build. When it stops compiling, fix the guide
// together with the example.

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	messageotel "github.com/sylphylabs/forge/contrib/otel/message"
	"github.com/sylphylabs/forge/transport/message"
)

// nopPublisher and nopSubscriber stand in for a broker adapter
// (contrib/message/...).
type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, string, *message.Message) error { return nil }

type nopSubscriber struct{}

func (nopSubscriber) Subscribe(context.Context, string, message.Handler) (message.Subscription, error) {
	return nopSubscription{}, nil
}

type nopSubscription struct{}

func (nopSubscription) Close(context.Context) error { return nil }

// Example_instrumentation mirrors "Asynchronous messages": the publisher is
// an explicit decorator around the adapter's Publisher, and the consumer is
// server middleware. WithSystem is yours to set — instrumentation never
// infers the broker from the wrapped implementation.
func Example_instrumentation() {
	provider := tracenoop.NewTracerProvider()
	next := nopPublisher{}
	subscriber := nopSubscriber{}

	publisher := messageotel.NewPublisher(next,
		messageotel.WithTracerProvider(provider),
		messageotel.WithPropagator(propagation.TraceContext{}),
		messageotel.WithSystem("nats"),
	)

	server := message.NewServer(subscriber,
		message.WithMiddleware(messageotel.Consumer(
			messageotel.WithTracerProvider(provider),
			messageotel.WithPropagator(propagation.TraceContext{}),
			messageotel.WithSystem("nats"),
		)),
	)

	_ = publisher
	_ = server
	fmt.Println("instrumented")
	// Output: instrumented
}
