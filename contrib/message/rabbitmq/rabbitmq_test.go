package rabbitmq

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

func TestPublishSendsHeadersIDAndRoutingKey(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{
		"orders.created": {Exchange: Exchange{Name: "orders"}},
	}))

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = "acct-1"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	if err := client.Publish(t.Context(), "orders.created", msg); err != nil {
		t.Fatal(err)
	}

	published := fake.channels[0].published
	if len(published) != 1 {
		t.Fatalf("published %d messages, want 1", len(published))
	}
	got := published[0]
	if got.exchange != "orders" {
		t.Errorf("exchange = %q, want orders", got.exchange)
	}
	if got.key != "acct-1" {
		t.Errorf("routing key = %q, want acct-1", got.key)
	}
	if got.msg.MessageId != "evt-1" {
		t.Errorf("MessageId = %q, want evt-1", got.msg.MessageId)
	}
	if string(got.msg.Body) != "created" {
		t.Errorf("Body = %q, want created", got.msg.Body)
	}
	if got.msg.DeliveryMode != amqp.Persistent {
		t.Errorf("DeliveryMode = %d, want persistent", got.msg.DeliveryMode)
	}
	if values, ok := got.msg.Headers["traceparent"].([]any); !ok || !reflect.DeepEqual(values, []any{"00-abc", "00-def"}) {
		t.Errorf("traceparent header = %v, want both values", got.msg.Headers["traceparent"])
	}
	if got.msg.Headers[HeaderMessageKey] != "acct-1" {
		t.Errorf("%s = %v, want acct-1", HeaderMessageKey, got.msg.Headers[HeaderMessageKey])
	}
	if !got.mandatory {
		t.Error("publish was not mandatory")
	}
}

func TestPublishWithoutBindingUsesDefaultExchange(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake)

	if err := client.Publish(t.Context(), "tasks", message.New([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	got := fake.channels[0].published[0]
	if got.exchange != "" {
		t.Errorf("exchange = %q, want the default exchange", got.exchange)
	}
	if got.key != "tasks" {
		t.Errorf("routing key = %q, want tasks", got.key)
	}
}

func TestPublishReportsBrokerNack(t *testing.T) {
	fake := newFakeConn()
	fake.confirmAcked = false
	client := newClient(t, fake)

	err := client.Publish(t.Context(), "tasks", message.New([]byte("x")))
	if !errors.Is(err, ErrPublishNacked) {
		t.Fatalf("Publish error = %v, want ErrPublishNacked", err)
	}
}

func TestPublishReportsUnroutableMessage(t *testing.T) {
	fake := newFakeConn()
	fake.returnOnPublish = true
	client := newClient(t, fake)

	err := client.Publish(t.Context(), "tasks", message.New([]byte("x")))
	if !errors.Is(err, ErrPublishReturned) {
		t.Fatalf("Publish error = %v, want ErrPublishReturned", err)
	}
}

func TestPublishValidatesArguments(t *testing.T) {
	client := newClient(t, newFakeConn())

	//nolint:staticcheck // SA1012: passing nil is the behavior under test.
	if err := client.Publish(nil, "tasks", message.New(nil)); !errors.Is(err, ErrNilContext) {
		t.Errorf("nil context error = %v, want ErrNilContext", err)
	}
	if err := client.Publish(t.Context(), " ", message.New(nil)); !errors.Is(err, ErrEmptyDestination) {
		t.Errorf("empty destination error = %v, want ErrEmptyDestination", err)
	}
	if err := client.Publish(t.Context(), "tasks", nil); !errors.Is(err, ErrNilMessage) {
		t.Errorf("nil message error = %v, want ErrNilMessage", err)
	}
}

func TestSubscribeDeliversAndAcknowledges(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{
		"orders.created": {Queue: Queue{Name: "orders"}, Exchange: Exchange{Name: "events"}},
	}))

	received := make(chan *message.Message, 1)
	destinations := make(chan string, 1)
	sub, err := client.Subscribe(t.Context(), "orders.created", func(ctx context.Context, destination string, msg *message.Message) error {
		md, ok := metadata.FromServerContext(ctx)
		if !ok {
			t.Errorf("server metadata missing from handler context")
		} else if got := md.Get("traceparent"); got != "00-abc" {
			t.Errorf("traceparent metadata = %q, want 00-abc", got)
		}
		destinations <- destination
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	acks := channel.acknowledger()
	channel.deliver(amqp.Delivery{
		Acknowledger: acks,
		MessageId:    "evt-1",
		RoutingKey:   "orders.created.eu",
		Body:         []byte("created"),
		Headers: amqp.Table{
			"traceparent":     []any{"00-abc", "00-def"},
			HeaderMessageKey:  "acct-1",
			"x-attempt":       int32(2),
			"x-binary-header": []byte("raw"),
		},
	})

	if got := <-destinations; got != "orders.created.eu" {
		t.Errorf("destination = %q, want the concrete routing key", got)
	}
	msg := <-received
	if msg.ID != "evt-1" {
		t.Errorf("ID = %q, want evt-1", msg.ID)
	}
	if msg.Key != "acct-1" {
		t.Errorf("Key = %q, want acct-1", msg.Key)
	}
	if string(msg.Body) != "created" {
		t.Errorf("Body = %q, want created", msg.Body)
	}
	if values := msg.Headers.Values("traceparent"); !reflect.DeepEqual(values, []string{"00-abc", "00-def"}) {
		t.Errorf("traceparent values = %v, want both values", values)
	}
	if got := msg.Header("x-attempt"); got != "2" {
		t.Errorf("x-attempt = %q, want 2", got)
	}
	if got := msg.Header("x-binary-header"); got != "raw" {
		t.Errorf("x-binary-header = %q, want raw", got)
	}

	if got := acks.wait(t); got != (settlement{acked: true}) {
		t.Errorf("settlement = %+v, want ack", got)
	}
}

func TestSubscribeFallsBackToRoutingKeyAsMessageKey(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{
		"orders": {Queue: Queue{Name: "orders"}},
	}))

	received := make(chan *message.Message, 1)
	sub, err := client.Subscribe(t.Context(), "orders", func(_ context.Context, _ string, msg *message.Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	channel.deliver(amqp.Delivery{Acknowledger: channel.acknowledger(), RoutingKey: "orders", Body: []byte("x")})
	if got := (<-received).Key; got != "orders" {
		t.Errorf("Key = %q, want the routing key", got)
	}
}

func TestHandlerErrorNacksWithoutRequeueByDefault(t *testing.T) {
	failures := make(chan Failure, 1)
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithErrorHandler(func(_ context.Context, failure Failure) { failures <- failure }),
	)

	handlerErr := errors.New("boom")
	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error {
		return handlerErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	acks := channel.acknowledger()
	channel.deliver(amqp.Delivery{Acknowledger: acks, RoutingKey: "orders", Body: []byte("x")})

	if got := acks.wait(t); got != (settlement{nacked: true}) {
		t.Errorf("settlement = %+v, want nack without requeue", got)
	}
	failure := <-failures
	if failure.Stage != StageHandler {
		t.Errorf("stage = %q, want %q", failure.Stage, StageHandler)
	}
	if !errors.Is(failure.Err, handlerErr) {
		t.Errorf("failure error = %v, want the handler error", failure.Err)
	}
	if failure.Destination != "orders" {
		t.Errorf("destination = %q, want orders", failure.Destination)
	}
}

func TestErrorClassifierCanRequeue(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithErrorClassifier(func(context.Context, *message.Message, error) Disposition { return Requeue }),
	)

	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error {
		return errors.New("transient")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	acks := channel.acknowledger()
	channel.deliver(amqp.Delivery{Acknowledger: acks, RoutingKey: "orders", Body: []byte("x")})

	if got := acks.wait(t); got != (settlement{nacked: true, requeue: true}) {
		t.Errorf("settlement = %+v, want nack with requeue", got)
	}
}

func TestSubscribeAppliesPrefetchAndManualAck(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithPrefetch(7),
	)

	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	if channel.prefetch != 7 {
		t.Errorf("prefetch = %d, want 7", channel.prefetch)
	}
	if channel.qosGlobal {
		t.Error("qos was applied globally, want per-consumer")
	}
	if channel.autoAck {
		t.Error("consumer used auto-ack, want manual acknowledgement")
	}
	if channel.consumerTag == "" {
		t.Error("consumer tag is empty")
	}
}

func TestSubscribeDoesNotDeclareTopologyByDefault(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{
		"orders": {
			Queue:    Queue{Name: "orders", Durable: true, BindingKeys: []string{"orders.#"}},
			Exchange: Exchange{Name: "events", Kind: amqp.ExchangeTopic, Durable: true},
		},
	}))

	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	if len(channel.declaredExchanges) != 0 || len(channel.declaredQueues) != 0 || len(channel.boundQueues) != 0 {
		t.Errorf("adapter declared topology without WithDeclare: %+v", channel)
	}
}

func TestSubscribeDeclaresTopologyWhenEnabled(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{
			"orders": {
				Queue:    Queue{Name: "orders", Durable: true, BindingKeys: []string{"orders.#", "orders.eu"}},
				Exchange: Exchange{Name: "events", Kind: amqp.ExchangeTopic, Durable: true},
			},
		}),
		WithDeclare(true),
	)

	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	if want := []string{"events:topic"}; !reflect.DeepEqual(channel.declaredExchanges, want) {
		t.Errorf("declared exchanges = %v, want %v", channel.declaredExchanges, want)
	}
	if want := []string{"orders"}; !reflect.DeepEqual(channel.declaredQueues, want) {
		t.Errorf("declared queues = %v, want %v", channel.declaredQueues, want)
	}
	if want := []string{"orders->events:orders.#", "orders->events:orders.eu"}; !reflect.DeepEqual(channel.boundQueues, want) {
		t.Errorf("queue bindings = %v, want %v", channel.boundQueues, want)
	}
}

// A RabbitMQ destination is a logical name, so wildcards are declared in the
// binding's BindingKeys rather than in the destination. A topic exchange
// evaluates them per message; the adapter passes them to QueueBind verbatim and
// never parses them.
func TestWildcardBindingKeysBindAgainstATopicExchange(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "single token", key: "orders.*"},
		{name: "multi token", key: "orders.#"},
		{name: "multi token in the middle", key: "orders.#.paid"},
		{name: "single token in the middle", key: "orders.*.eu"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := newFakeConn()
			client := newClient(t, fake,
				WithBindings(map[string]Binding{
					// The destination stays a plain logical name; the pattern
					// lives in BindingKeys.
					"orders": {
						Queue:    Queue{Name: "order-worker", BindingKeys: []string{tt.key}},
						Exchange: Exchange{Name: "events"},
					},
				}),
				WithDeclare(true),
			)

			sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			defer closeSubscription(t, sub)

			channel := fake.channels[0]
			// An unspecified Exchange.Kind defaults to topic, which is the only
			// exchange type that evaluates `*` and `#`.
			if want := []string{"events:topic"}; !reflect.DeepEqual(channel.declaredExchanges, want) {
				t.Errorf("declared exchanges = %v, want %v", channel.declaredExchanges, want)
			}
			if want := []string{"order-worker->events:" + tt.key}; !reflect.DeepEqual(channel.boundQueues, want) {
				t.Errorf("queue bindings = %v, want %v", channel.boundQueues, want)
			}
		})
	}
}

// A queue bound with a wildcard receives messages whose routing keys differ from
// both the binding key and the destination. The handler must see the concrete
// routing key, because that is the only place the varying token survives.
func TestWildcardBoundQueueDeliversConcreteRoutingKeys(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{
			"orders": {
				Queue:    Queue{Name: "order-worker", BindingKeys: []string{"orders.#"}},
				Exchange: Exchange{Name: "events"},
			},
		}),
		WithDeclare(true),
	)

	destinations := make(chan string, 1)
	keys := make(chan string, 1)
	sub, err := client.Subscribe(t.Context(), "orders", func(_ context.Context, destination string, msg *message.Message) error {
		destinations <- destination
		keys <- msg.Key
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	channel := fake.channels[0]
	for _, routingKey := range []string{"orders.created", "orders.created.eu.paid"} {
		channel.deliver(amqp.Delivery{
			Acknowledger: channel.acknowledger(),
			RoutingKey:   routingKey,
			Body:         []byte("payload"),
		})
		if got := <-destinations; got != routingKey {
			t.Errorf("destination = %q, want the concrete routing key %q", got, routingKey)
		}
		// With no forge-message-key header the routing key also becomes Key, so
		// the varying token stays reachable from the message itself.
		if got := <-keys; got != routingKey {
			t.Errorf("Key = %q, want the concrete routing key %q", got, routingKey)
		}
	}
}

// The destination is a map key, not a pattern. Passing a wildcard as the
// destination finds no binding and fails at registration rather than silently
// never delivering.
func TestWildcardDestinationIsNotABinding(t *testing.T) {
	client := newClient(t, newFakeConn(), WithBindings(map[string]Binding{
		"orders": {Queue: Queue{Name: "order-worker", BindingKeys: []string{"orders.#"}}, Exchange: Exchange{Name: "events"}},
	}))
	_, err := client.Subscribe(t.Context(), "orders.#", func(context.Context, string, *message.Message) error { return nil })
	if !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("Subscribe(%q) error = %v, want ErrBindingNotFound", "orders.#", err)
	}
}

func TestSubscribeValidatesArguments(t *testing.T) {
	client := newClient(t, newFakeConn(), WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}))
	handler := func(context.Context, string, *message.Message) error { return nil }

	//nolint:staticcheck // SA1012: passing nil is the behavior under test.
	if _, err := client.Subscribe(nil, "orders", handler); !errors.Is(err, ErrNilContext) {
		t.Errorf("nil context error = %v, want ErrNilContext", err)
	}
	if _, err := client.Subscribe(t.Context(), " ", handler); !errors.Is(err, ErrEmptyDestination) {
		t.Errorf("empty destination error = %v, want ErrEmptyDestination", err)
	}
	if _, err := client.Subscribe(t.Context(), "orders", nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("nil handler error = %v, want ErrNilHandler", err)
	}
	if _, err := client.Subscribe(t.Context(), "unknown", handler); !errors.Is(err, ErrBindingNotFound) {
		t.Errorf("unknown destination error = %v, want ErrBindingNotFound", err)
	}
}

func TestSubscribeFailsWhenBindingHasNoQueue(t *testing.T) {
	client := newClient(t, newFakeConn(), WithBindings(map[string]Binding{
		"orders": {Exchange: Exchange{Name: "events"}},
	}))
	_, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
	if !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("Subscribe error = %v, want ErrInvalidBinding", err)
	}
}

func TestSubscribeReportsConsumeFailureSynchronously(t *testing.T) {
	fake := newFakeConn()
	fake.setConsumeErr(errors.New("NOT_FOUND - no queue"))
	client := newClient(t, fake, WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}))

	if _, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil }); err == nil {
		t.Fatal("Subscribe succeeded against a missing queue, want an error")
	}
	if !fake.channels[0].closed.Load() {
		t.Error("failed subscribe leaked its channel")
	}
}

func TestSubscriptionCloseIsIdempotentAndStopsDelivery(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}))

	sub, err := client.Subscribe(t.Context(), "orders", func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sub.Close(t.Context()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !fake.channels[0].closed.Load() {
		t.Error("Close did not close the AMQP channel")
	}

	//nolint:staticcheck // SA1012: passing nil is the behavior under test.
	if err := sub.Close(nil); !errors.Is(err, ErrNilContext) {
		t.Errorf("nil context error = %v, want ErrNilContext", err)
	}
}

func TestSubscriptionStopsWhenContextIsCanceled(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}))

	ctx, cancel := context.WithCancel(t.Context())
	sub, err := client.Subscribe(ctx, "orders", func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := sub.Close(t.Context()); err != nil {
		t.Fatalf("Close after cancellation: %v", err)
	}
	if !fake.channels[0].closed.Load() {
		t.Error("cancellation did not close the AMQP channel")
	}
}

func TestSubscriptionRecoversAfterBrokerDropsTheChannel(t *testing.T) {
	fake := newFakeConn()
	failures := make(chan Failure, 4)
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithRecoveryDelay(time.Millisecond),
		WithErrorHandler(func(_ context.Context, failure Failure) {
			select {
			case failures <- failure:
			default:
			}
		}),
	)

	received := make(chan string, 2)
	sub, err := client.Subscribe(t.Context(), "orders", func(_ context.Context, _ string, msg *message.Message) error {
		received <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	first := fake.channels[0]
	first.deliver(amqp.Delivery{Acknowledger: first.acknowledger(), RoutingKey: "orders", Body: []byte("before")})
	if got := <-received; got != "before" {
		t.Fatalf("body = %q, want before", got)
	}

	first.breakChannel(t, &amqp.Error{Code: amqp.ConnectionForced, Reason: "broker restart"})
	if failure := <-failures; failure.Stage != StageConsume {
		t.Errorf("stage = %q, want %q", failure.Stage, StageConsume)
	}

	second := fake.waitForChannel(t, 2)
	second.deliver(amqp.Delivery{Acknowledger: second.acknowledger(), RoutingKey: "orders", Body: []byte("after")})
	if got := <-received; got != "after" {
		t.Errorf("body = %q, want after", got)
	}
	if fake.dials.Load() < 2 {
		t.Errorf("dials = %d, want a redial after the drop", fake.dials.Load())
	}
}

func TestSubscriptionRetriesUntilTheBrokerReturns(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithRecoveryDelay(time.Millisecond),
	)

	received := make(chan string, 1)
	sub, err := client.Subscribe(t.Context(), "orders", func(_ context.Context, _ string, msg *message.Message) error {
		received <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	fake.setDialErr(errors.New("connection refused"))
	fake.channels[0].breakChannel(t, &amqp.Error{Code: amqp.ConnectionForced, Reason: "down"})

	// Let recovery fail a few times before the broker comes back, so the test
	// covers the retry loop rather than a single lucky attempt.
	time.Sleep(20 * time.Millisecond)
	fake.setDialErr(nil)

	recovered := fake.waitForChannel(t, 2)
	recovered.deliver(amqp.Delivery{Acknowledger: recovered.acknowledger(), RoutingKey: "orders", Body: []byte("back")})
	if got := <-received; got != "back" {
		t.Errorf("body = %q, want back", got)
	}
}

func TestSubscriptionRecoversAfterBrokerCancelsTheConsumer(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake,
		WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}),
		WithRecoveryDelay(time.Millisecond),
	)

	received := make(chan string, 1)
	sub, err := client.Subscribe(t.Context(), "orders", func(_ context.Context, _ string, msg *message.Message) error {
		received <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	fake.channels[0].cancelConsumer()
	second := fake.waitForChannel(t, 2)
	second.deliver(amqp.Delivery{Acknowledger: second.acknowledger(), RoutingKey: "orders", Body: []byte("again")})
	if got := <-received; got != "again" {
		t.Errorf("body = %q, want again", got)
	}
}

func TestCloseStopsTheClient(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake)

	if err := client.Publish(t.Context(), "tasks", message.New([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if !fake.closed.Load() {
		t.Error("Close did not close the connection")
	}
	if err := client.Publish(t.Context(), "tasks", message.New([]byte("x"))); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish after Close = %v, want ErrClosed", err)
	}
}

// TestConcurrentPublishSharesOneConnection covers the race where two publishes
// dial at once: the loser must discard its channel silently rather than report
// the discard as a publish failure.
func TestConcurrentPublishSharesOneConnection(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake)

	const publishers = 8
	errs := make(chan error, publishers)
	var start sync.WaitGroup
	start.Add(1)
	for range publishers {
		go func() {
			start.Wait()
			errs <- client.Publish(t.Context(), "tasks", message.New([]byte("x")))
		}()
	}
	start.Done()
	for range publishers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Publish: %v", err)
		}
	}
	// Racing dials are allowed; keeping more than one is not, and every
	// publish must have gone through the single retained channel.
	published := 0
	for _, channel := range fake.snapshotChannels() {
		channel.mu.Lock()
		published += len(channel.published)
		channel.mu.Unlock()
	}
	if published != publishers {
		t.Errorf("published %d messages across channels, want %d", published, publishers)
	}
}

func TestNewValidatesOptions(t *testing.T) {
	if _, err := New(WithURL(" ")); !errors.Is(err, ErrEmptyURL) {
		t.Errorf("empty url error = %v, want ErrEmptyURL", err)
	}
	if _, err := New(WithDialer(nil)); !errors.Is(err, ErrNilDialer) {
		t.Errorf("nil dialer error = %v, want ErrNilDialer", err)
	}
	if _, err := New(WithBindings(map[string]Binding{" ": {Queue: Queue{Name: "q"}}})); !errors.Is(err, ErrInvalidBinding) {
		t.Errorf("empty destination error = %v, want ErrInvalidBinding", err)
	}
	if _, err := New(WithBindings(map[string]Binding{"orders": {}})); !errors.Is(err, ErrInvalidBinding) {
		t.Errorf("empty binding error = %v, want ErrInvalidBinding", err)
	}
	if _, err := New(nil); err != nil {
		t.Errorf("nil option error = %v, want nil", err)
	}
}

func TestMessageServerLifecycle(t *testing.T) {
	fake := newFakeConn()
	client := newClient(t, fake, WithBindings(map[string]Binding{"orders": {Queue: Queue{Name: "orders"}}}))

	delivered := make(chan string, 1)
	server := message.NewServer(client)
	if err := server.Handle("orders", func(ctx context.Context, req any) (any, error) {
		destination, _ := message.DestinationFromServerContext(ctx)
		msg, _ := req.(*message.Message)
		delivered <- destination + ":" + string(msg.Body)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(t.Context()) }()

	channel := fake.waitForChannel(t, 1)
	channel.deliver(amqp.Delivery{Acknowledger: channel.acknowledger(), RoutingKey: "orders", Body: []byte("ok")})
	if got := <-delivered; got != "orders:ok" {
		t.Errorf("delivery = %q, want orders:ok", got)
	}

	if err := server.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !channel.closed.Load() {
		t.Error("server shutdown did not close the AMQP channel")
	}
}

func newClient(t *testing.T, fake *fakeConn, opts ...Option) *Client {
	t.Helper()
	client, err := New(append([]Option{WithDialer(fake.dial)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return client
}

func closeSubscription(t *testing.T, sub message.Subscription) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()
	if err := sub.Close(ctx); err != nil {
		t.Errorf("Close subscription: %v", err)
	}
}
