package jetstream

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	natsjs "github.com/nats-io/nats.go/jetstream"

	"github.com/openkratos/kratos/metadata"
	"github.com/openkratos/kratos/transport/message"
)

const (
	testStream   = "ORDERS"
	testSubject  = "orders.created"
	testConsumer = "order-worker"
)

func TestPublisherAckDeduplicationAndDelivery(t *testing.T) {
	js := runJetStream(t)
	stream, consumer := provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}

	received := make(chan *message.Message, 1)
	subscriber, err := NewSubscriber(js, testBindings())
	if err != nil {
		t.Fatal(err)
	}
	sub, err := subscriber.Subscribe(t.Context(), testSubject, func(ctx context.Context, subject string, msg *message.Message) error {
		if subject != testSubject {
			t.Errorf("subject = %q, want %q", subject, testSubject)
		}
		md, ok := metadata.FromServerContext(ctx)
		if !ok {
			t.Error("server metadata missing from handler context")
		} else if got := md.Get("traceparent"); got != "00-abc" {
			t.Errorf("traceparent metadata = %q, want 00-abc", got)
		}
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = "order-1"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	ack, err := publisher.PublishAck(timeoutContext(t), testSubject, msg)
	if err != nil {
		t.Fatal(err)
	}
	if ack.Stream != testStream || ack.Sequence != 1 || ack.Duplicate {
		t.Fatalf("first PubAck = %+v, want stream %s sequence 1 non-duplicate", ack, testStream)
	}

	got := receiveMessage(t, received)
	if got.ID != msg.ID || got.Key != msg.Key || string(got.Body) != "created" {
		t.Fatalf("message = %+v, want ID %q key %q body created", got, msg.ID, msg.Key)
	}
	if values := got.Headers.Values("traceparent"); !reflect.DeepEqual(values, []string{"00-abc", "00-def"}) {
		t.Errorf("traceparent values = %v, want both values", values)
	}
	waitFor(t, func() bool {
		info, infoErr := consumer.Info(timeoutContext(t))
		return infoErr == nil && info.NumAckPending == 0 && info.AckFloor.Stream == 1
	})

	duplicate, err := publisher.PublishAck(timeoutContext(t), testSubject, msg)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.Sequence != 1 {
		t.Fatalf("duplicate PubAck = %+v, want duplicate sequence 1", duplicate)
	}
	info, err := stream.Info(timeoutContext(t))
	if err != nil {
		t.Fatal(err)
	}
	if info.State.Msgs != 1 {
		t.Fatalf("stream messages = %d, want 1 after duplicate publish", info.State.Msgs)
	}
}

func TestSubscriberRetriesHandlerFailureWithDelay(t *testing.T) {
	js := runJetStream(t)
	_, consumer := provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("retry order")
	failures := make(chan Failure, 1)
	attempts := make(chan time.Time, 2)
	subscriber, err := NewSubscriber(
		js,
		testBindings(),
		WithRetryDelay(75*time.Millisecond),
		WithErrorHandler(func(_ context.Context, failure Failure) { failures <- failure }),
	)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	sub, err := subscriber.Subscribe(t.Context(), testSubject, func(context.Context, string, *message.Message) error {
		count++
		attempts <- time.Now()
		if count == 1 {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("created"))); err != nil {
		t.Fatal(err)
	}
	first := receiveTime(t, attempts)
	second := receiveTime(t, attempts)
	if elapsed := second.Sub(first); elapsed < 50*time.Millisecond {
		t.Fatalf("redelivery delay = %v, want at least 50ms", elapsed)
	}
	select {
	case failure := <-failures:
		if failure.Stage != StageHandler || failure.Destination != testSubject || !errors.Is(failure.Err, wantErr) {
			t.Fatalf("failure = %+v, want handler failure wrapping %v", failure, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler failure report")
	}
	waitFor(t, func() bool {
		info, infoErr := consumer.Info(timeoutContext(t))
		return infoErr == nil && info.NumAckPending == 0 && info.AckFloor.Stream == 1
	})
}

func TestSubscriberTerminatesPermanentFailure(t *testing.T) {
	js := runJetStream(t)
	_, consumer := provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	attempts := make(chan struct{}, 2)
	failures := make(chan Failure, 1)
	subscriber, err := NewSubscriber(
		js,
		testBindings(),
		WithErrorClassifier(func(context.Context, *message.Message, error) ErrorDisposition { return Terminate }),
		WithErrorHandler(func(_ context.Context, failure Failure) { failures <- failure }),
	)
	if err != nil {
		t.Fatal(err)
	}
	sub, err := subscriber.Subscribe(t.Context(), testSubject, func(context.Context, string, *message.Message) error {
		attempts <- struct{}{}
		return errors.New("invalid payload")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("bad"))); err != nil {
		t.Fatal(err)
	}
	receiveSignal(t, attempts)
	select {
	case failure := <-failures:
		if failure.Stage != StageHandler {
			t.Fatalf("failure stage = %q, want %q", failure.Stage, StageHandler)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for permanent failure report")
	}
	waitFor(t, func() bool {
		info, infoErr := consumer.Info(timeoutContext(t))
		return infoErr == nil && info.NumAckPending == 0
	})
	select {
	case <-attempts:
		t.Fatal("terminated message was redelivered")
	case <-time.After(250 * time.Millisecond):
	}
}

func TestSubscriberRequiresExistingBindingAndConsumer(t *testing.T) {
	js := runJetStream(t)
	if _, err := js.CreateStream(timeoutContext(t), natsjs.StreamConfig{
		Name:     testStream,
		Subjects: []string{testSubject},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := NewPublisher(nil); !errors.Is(err, ErrNilJetStream) {
		t.Fatalf("NewPublisher(nil) error = %v, want ErrNilJetStream", err)
	}
	if _, err := NewSubscriber(nil, nil); !errors.Is(err, ErrNilJetStream) {
		t.Fatalf("NewSubscriber(nil) error = %v, want ErrNilJetStream", err)
	}
	if _, err := NewSubscriber(js, map[string]Binding{"": {Stream: testStream, Consumer: testConsumer}}); !errors.Is(err, ErrInvalidBinding) {
		t.Fatalf("invalid binding error = %v, want ErrInvalidBinding", err)
	}
	subscriber, err := NewSubscriber(js, testBindings())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subscriber.Subscribe(t.Context(), "orders.cancelled", func(context.Context, string, *message.Message) error { return nil }); !errors.Is(err, ErrBindingNotFound) {
		t.Fatalf("missing binding error = %v, want ErrBindingNotFound", err)
	}
	if _, err := subscriber.Subscribe(t.Context(), testSubject, func(context.Context, string, *message.Message) error { return nil }); !errors.Is(err, natsjs.ErrConsumerNotFound) {
		t.Fatalf("missing consumer error = %v, want ErrConsumerNotFound", err)
	}
}

func TestSubscriptionDrainWaitsForInFlightHandler(t *testing.T) {
	js := runJetStream(t)
	_, _ = provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	subscriber, err := NewSubscriber(js, testBindings(), WithConsumeOptions(natsjs.PullMaxMessages(1)))
	if err != nil {
		t.Fatal(err)
	}
	subCtx, cancel := context.WithCancel(t.Context())
	sub, err := subscriber.Subscribe(subCtx, testSubject, func(context.Context, string, *message.Message) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("created"))); err != nil {
		t.Fatal(err)
	}
	receiveSignal(t, started)

	closed := make(chan error, 1)
	go func() { closed <- sub.Close(timeoutContext(t)) }()
	select {
	case err := <-closed:
		t.Fatalf("Close returned before in-flight handler completed: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := receiveError(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	cancel()
}

func TestSubscriptionCancellationStopsDelivery(t *testing.T) {
	js := runJetStream(t)
	_, _ = provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	delivered := make(chan struct{}, 1)
	subscriber, err := NewSubscriber(js, testBindings())
	if err != nil {
		t.Fatal(err)
	}
	subCtx, cancel := context.WithCancel(t.Context())
	sub, err := subscriber.Subscribe(subCtx, testSubject, func(context.Context, string, *message.Message) error {
		delivered <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	inner := sub.(*subscription)
	closed := inner.consume.Closed()
	cancel()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("consumption did not stop after context cancellation")
	}

	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("created"))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-delivered:
		t.Fatal("handler ran after subscription cancellation")
	case <-time.After(150 * time.Millisecond):
	}
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}
}

func TestSubscriptionCloseTimeoutStopsConsumption(t *testing.T) {
	js := runJetStream(t)
	_, _ = provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	subscriber, err := NewSubscriber(js, testBindings(), WithConsumeOptions(natsjs.PullMaxMessages(1)))
	if err != nil {
		t.Fatal(err)
	}
	sub, err := subscriber.Subscribe(t.Context(), testSubject, func(context.Context, string, *message.Message) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("created"))); err != nil {
		t.Fatal(err)
	}
	receiveSignal(t, started)

	closeCtx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	err = sub.Close(closeCtx)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want context deadline exceeded", err)
	}
	close(release)
	inner := sub.(*subscription)
	select {
	case <-inner.consume.Closed():
	case <-time.After(time.Second):
		t.Fatal("consumption did not stop after Close timeout")
	}
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatalf("Close after timeout = %v, want nil", err)
	}
}

func TestMessageServerLifecycleWithJetStream(t *testing.T) {
	js := runJetStream(t)
	_, _ = provision(t, js, testStream, testSubject, testConsumer)
	publisher, err := NewPublisher(js)
	if err != nil {
		t.Fatal(err)
	}
	subscriber, err := NewSubscriber(js, testBindings())
	if err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	server := message.NewServer(&readySubscriber{Subscriber: subscriber, ready: ready})
	delivered := make(chan string, 1)
	if err := server.Handle(testSubject, func(_ context.Context, subject string, msg *message.Message) error {
		delivered <- subject + ":" + string(msg.Body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(t.Context()) }()
	receiveSignal(t, ready)

	if err := publisher.Publish(timeoutContext(t), testSubject, message.New([]byte("ok"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != testSubject+":ok" {
			t.Fatalf("delivery = %q, want %q", got, testSubject+":ok")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message server delivery")
	}
	if err := server.Stop(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := receiveError(t, startErr); err != nil {
		t.Fatal(err)
	}
}

func runJetStream(t *testing.T) natsjs.JetStream {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		NoLog:     true,
		NoSigs:    true,
		JetStream: true,
		StoreDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(time.Second) {
		s.Shutdown()
		t.Fatal("JetStream server did not become ready")
	}
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})

	conn, err := natsgo.Connect(s.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	js, err := natsjs.New(conn)
	if err != nil {
		t.Fatal(err)
	}
	return js
}

func provision(t *testing.T, js natsjs.JetStream, streamName, subject, consumerName string) (natsjs.Stream, natsjs.Consumer) {
	t.Helper()
	stream, err := js.CreateStream(timeoutContext(t), natsjs.StreamConfig{
		Name:       streamName,
		Subjects:   []string{subject},
		Duplicates: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := stream.CreateConsumer(timeoutContext(t), natsjs.ConsumerConfig{
		Name:          consumerName,
		Durable:       consumerName,
		AckPolicy:     natsjs.AckExplicitPolicy,
		AckWait:       100 * time.Millisecond,
		MaxDeliver:    5,
		FilterSubject: subject,
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream, consumer
}

func testBindings() map[string]Binding {
	return map[string]Binding{
		testSubject: {Stream: testStream, Consumer: testConsumer},
	}
}

func timeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func closeSubscription(t *testing.T, sub message.Subscription) {
	t.Helper()
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Error(err)
	}
}

func receiveMessage(t *testing.T, ch <-chan *message.Message) *message.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func receiveTime(t *testing.T, ch <-chan time.Time) time.Time {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
		return time.Time{}
	}
}

func receiveSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func receiveError(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for result")
		return nil
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

type readySubscriber struct {
	*Subscriber
	ready chan struct{}
}

func (s *readySubscriber) Subscribe(ctx context.Context, destination string, handler message.Handler) (message.Subscription, error) {
	sub, err := s.Subscriber.Subscribe(ctx, destination, handler)
	if err == nil {
		close(s.ready)
	}
	return sub, err
}
