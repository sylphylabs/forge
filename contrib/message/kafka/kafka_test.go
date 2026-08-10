package kafka

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

func TestPublishSubscribeWithHeaders(t *testing.T) {
	seeds := runCluster(t, "accounts.created")
	publisher := newPublisher(t, seeds)
	subscriber := newSubscriber(t, seeds, nil)
	received := make(chan *message.Message, 1)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	sub, err := subscriber.Subscribe(subCtx, "accounts.created", func(ctx context.Context, topic string, msg *message.Message) error {
		if topic != "accounts.created" {
			t.Errorf("topic = %q, want accounts.created", topic)
		}
		md, ok := metadata.FromServerContext(ctx)
		if !ok {
			t.Errorf("server metadata missing from handler context")
		} else if got := md.Get("traceparent"); got != "00-abc" {
			t.Errorf("traceparent metadata = %q, want 00-abc", got)
		}
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(timeoutContext(t))

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = "acct-1"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	if err := publisher.Publish(timeoutContext(t), "accounts.created", msg); err != nil {
		t.Fatal(err)
	}

	got := receiveMessage(t, received)
	if got.ID != "evt-1" {
		t.Errorf("ID = %q, want evt-1", got.ID)
	}
	if got.Key != "acct-1" {
		t.Errorf("Key = %q, want acct-1", got.Key)
	}
	if string(got.Body) != "created" {
		t.Errorf("Body = %q, want created", got.Body)
	}
	if values := got.Headers.Values("traceparent"); !reflect.DeepEqual(values, []string{"00-abc", "00-def"}) {
		t.Errorf("traceparent values = %v, want both values", values)
	}
}

func TestMessageServerLifecycleWithKafka(t *testing.T) {
	seeds := runCluster(t, "orders.created")
	publisher := newPublisher(t, seeds)
	ready := make(chan struct{})
	subscriber := newSubscriber(t, seeds, nil)
	server := message.NewServer(&readySubscriber{Subscriber: subscriber, ready: ready})
	delivered := make(chan string, 1)
	if err := server.Handle("orders.created", func(ctx context.Context, req any) (any, error) {
		topic, _ := message.DestinationFromServerContext(ctx)
		msg, _ := req.(*message.Message)
		delivered <- topic + ":" + string(msg.Body)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() {
		startErr <- server.Start(t.Context())
	}()
	defer func() {
		if err := server.Stop(timeoutContext(t)); err != nil && !errors.Is(err, message.ErrStopped) {
			t.Fatalf("Stop: %v", err)
		}
		if err := <-startErr; err != nil {
			t.Fatalf("Start: %v", err)
		}
	}()

	select {
	case <-ready:
	case <-time.After(readyTimeout):
		t.Fatal("timed out waiting for message server subscription")
	}
	if err := publisher.Publish(timeoutContext(t), "orders.created", message.New([]byte("ok"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != "orders.created:ok" {
			t.Fatalf("delivery = %q, want orders.created:ok", got)
		}
	case <-time.After(deliveryTimeout):
		t.Fatal("timed out waiting for delivery")
	}
}

// A handler error must leave the failing offset uncommitted so a later member
// re-reads it, and must not commit the records that follow it in the batch.
func TestHandlerErrorStopsBatchAndLeavesOffsetUncommitted(t *testing.T) {
	seeds := runCluster(t, "events")
	publisher := newPublisher(t, seeds)
	wantErr := errors.New("handler failed")
	failures := make(chan Failure, 4)
	subscriber := newSubscriber(t, seeds, func(_ context.Context, failure Failure) {
		failures <- failure
	})

	for _, body := range []string{"first", "second", "third"} {
		msg := message.New([]byte(body))
		msg.Key = "same-partition"
		if err := publisher.Publish(timeoutContext(t), "events", msg); err != nil {
			t.Fatal(err)
		}
	}

	seen := make(chan string, 8)
	subCtx, cancel := context.WithCancel(t.Context())
	sub, err := subscriber.Subscribe(subCtx, "events", func(_ context.Context, _ string, msg *message.Message) error {
		body := string(msg.Body)
		seen <- body
		if body == "second" {
			return wantErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := receiveString(t, seen); got != "first" {
		t.Fatalf("first delivery = %q, want first", got)
	}
	if got := receiveString(t, seen); got != "second" {
		t.Fatalf("second delivery = %q, want second", got)
	}
	select {
	case failure := <-failures:
		if failure.Stage != StageHandler {
			t.Fatalf("failure stage = %q, want %q", failure.Stage, StageHandler)
		}
		if !errors.Is(failure.Err, wantErr) {
			t.Fatalf("reported error = %v, want %v", failure.Err, wantErr)
		}
		if string(failure.Message.Body) != "second" {
			t.Fatalf("failure body = %q, want second", failure.Message.Body)
		}
	case <-time.After(deliveryTimeout):
		t.Fatal("timed out waiting for handler failure")
	}

	cancel()
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}

	// A fresh member of the same group resumes at the failing record, proving
	// the successful prefix committed and the failure did not.
	resumed := make(chan string, 8)
	resumeCtx, resumeCancel := context.WithCancel(t.Context())
	defer resumeCancel()
	resumeSub, err := subscriber.Subscribe(resumeCtx, "events", func(_ context.Context, _ string, msg *message.Message) error {
		resumed <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resumeSub.Close(timeoutContext(t))
	if got := receiveString(t, resumed); got != "second" {
		t.Fatalf("resumed delivery = %q, want second", got)
	}
}

func TestSubscriptionCloseStopsDelivery(t *testing.T) {
	seeds := runCluster(t, "cancel.me")
	publisher := newPublisher(t, seeds)
	subscriber := newSubscriber(t, seeds, nil)
	var delivered atomic.Int64
	subCtx, cancel := context.WithCancel(t.Context())
	sub, err := subscriber.Subscribe(subCtx, "cancel.me", func(context.Context, string, *message.Message) error {
		delivered.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}

	if err := publisher.Publish(timeoutContext(t), "cancel.me", message.New([]byte("late"))); err != nil {
		t.Fatal(err)
	}
	before := delivered.Load()
	time.Sleep(200 * time.Millisecond)
	if got := delivered.Load(); got != before {
		t.Fatalf("handler ran %d times after close", got-before)
	}
}

func TestPublisherOwnershipAndValidation(t *testing.T) {
	seeds := runCluster(t, "owned")
	owned, err := NewPublisher(WithPublisherSeedBrokers(seeds...))
	if err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Publish(timeoutContext(t), "owned", message.New(nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Publish error = %v, want ErrClosed", err)
	}

	client, err := kgo.NewClient(kgo.SeedBrokers(seeds...))
	if err != nil {
		t.Fatal(err)
	}
	publisher, err := NewPublisher(WithPublisherClient(client))
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Ping(timeoutContext(t)); err != nil {
		t.Fatalf("application-owned client was closed: %v", err)
	}
	client.Close()

	if _, err := NewPublisher(WithPublisherClient(nil)); !errors.Is(err, ErrNilClient) {
		t.Fatalf("nil client NewPublisher error = %v, want ErrNilClient", err)
	}
	if _, err := NewPublisher(); !errors.Is(err, ErrNoSeedBrokers) {
		t.Fatalf("seedless NewPublisher error = %v, want ErrNoSeedBrokers", err)
	}
	//nolint:staticcheck // SA1012: passing nil is the behavior under test.
	if err := publisher.Publish(nil, "events", message.New(nil)); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context Publish error = %v, want ErrNilContext", err)
	}
	if err := publisher.Publish(timeoutContext(t), "", message.New(nil)); !errors.Is(err, ErrEmptyTopic) {
		t.Fatalf("empty topic Publish error = %v, want ErrEmptyTopic", err)
	}
	if err := publisher.Publish(timeoutContext(t), "events", nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil message Publish error = %v, want ErrNilMessage", err)
	}
}

func TestSubscriberValidation(t *testing.T) {
	if _, err := NewSubscriber("", WithSubscriberSeedBrokers("127.0.0.1:9092")); !errors.Is(err, ErrEmptyGroup) {
		t.Fatalf("empty group NewSubscriber error = %v, want ErrEmptyGroup", err)
	}
	if _, err := NewSubscriber("group"); !errors.Is(err, ErrNoSeedBrokers) {
		t.Fatalf("seedless NewSubscriber error = %v, want ErrNoSeedBrokers", err)
	}
	subscriber, err := NewSubscriber("group", WithSubscriberSeedBrokers("127.0.0.1:9092"))
	if err != nil {
		t.Fatal(err)
	}
	//nolint:staticcheck // SA1012: passing nil is the behavior under test.
	if _, err := subscriber.Subscribe(nil, "events", func(context.Context, string, *message.Message) error { return nil }); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context Subscribe error = %v, want ErrNilContext", err)
	}
	if _, err := subscriber.Subscribe(t.Context(), "", func(context.Context, string, *message.Message) error { return nil }); !errors.Is(err, ErrEmptyTopic) {
		t.Fatalf("empty topic Subscribe error = %v, want ErrEmptyTopic", err)
	}
	if _, err := subscriber.Subscribe(t.Context(), "events", nil); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("nil handler Subscribe error = %v, want ErrNilHandler", err)
	}
}

func TestRecordConversionRoundTrip(t *testing.T) {
	msg := message.New([]byte("payload"))
	msg.ID = "evt-9"
	msg.Key = "k"
	msg.SetHeader("Trace", "one")
	msg.AddHeader("Trace", "two")

	record := toRecord("topic", msg)
	if record.Topic != "topic" {
		t.Errorf("record topic = %q, want topic", record.Topic)
	}
	if string(record.Key) != "k" {
		t.Errorf("record key = %q, want k", record.Key)
	}
	if string(record.Value) != "payload" {
		t.Errorf("record value = %q, want payload", record.Value)
	}

	got := fromRecord(record)
	if got.ID != "evt-9" {
		t.Errorf("ID = %q, want evt-9", got.ID)
	}
	if got.Key != "k" {
		t.Errorf("Key = %q, want k", got.Key)
	}
	if string(got.Body) != "payload" {
		t.Errorf("Body = %q, want payload", got.Body)
	}
	if values := got.Headers.Values("trace"); !reflect.DeepEqual(values, []string{"one", "two"}) {
		t.Errorf("trace values = %v, want both values", values)
	}
}

// An empty Message must not send an empty key: Kafka distinguishes a nil key,
// which round-robins partitions, from a present empty key, which pins one.
func TestEmptyKeyStaysNil(t *testing.T) {
	record := toRecord("topic", message.New([]byte("body")))
	if record.Key != nil {
		t.Fatalf("record key = %v, want nil", record.Key)
	}
	if len(record.Headers) != 0 {
		t.Fatalf("record headers = %v, want none", record.Headers)
	}
}

const (
	readyTimeout    = 20 * time.Second
	deliveryTimeout = 20 * time.Second
)

func runCluster(t *testing.T, topics ...string) []string {
	t.Helper()
	cluster, err := kfake.NewCluster(
		kfake.SeedTopics(1, topics...),
		kfake.NumBrokers(1),
		// Consumers must rejoin quickly when a test closes a subscription and
		// starts another in the same group.
		kfake.GroupMinSessionTimeout(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cluster.Close)
	return cluster.ListenAddrs()
}

func newPublisher(t *testing.T, seeds []string) *Publisher {
	t.Helper()
	publisher, err := NewPublisher(WithPublisherSeedBrokers(seeds...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := publisher.Close(); err != nil {
			t.Error(err)
		}
	})
	return publisher
}

func newSubscriber(t *testing.T, seeds []string, onError ErrorHandler) *Subscriber {
	t.Helper()
	opts := []SubscriberOption{
		WithSubscriberSeedBrokers(seeds...),
		WithSubscriberClientOptions(
			kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
			kgo.SessionTimeout(6*time.Second),
		),
		WithMaxPollRecords(16),
	}
	if onError != nil {
		opts = append(opts, WithErrorHandler(onError))
	}
	subscriber, err := NewSubscriber("forge-test-group", opts...)
	if err != nil {
		t.Fatal(err)
	}
	return subscriber
}

func timeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func receiveMessage(t *testing.T, ch <-chan *message.Message) *message.Message {
	t.Helper()
	select {
	case msg := <-ch:
		return msg
	case <-time.After(deliveryTimeout):
		t.Fatal("timed out waiting for message")
		return nil
	}
}

func receiveString(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(deliveryTimeout):
		t.Fatal("timed out waiting for delivery")
		return ""
	}
}

type readySubscriber struct {
	*Subscriber
	ready chan struct{}
}

func (s *readySubscriber) Subscribe(ctx context.Context, topic string, handler message.Handler) (message.Subscription, error) {
	sub, err := s.Subscriber.Subscribe(ctx, topic, handler)
	if err == nil {
		close(s.ready)
	}
	return sub, err
}
