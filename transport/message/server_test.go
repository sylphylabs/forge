package message

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

type fakeSubscriber struct {
	mu          sync.Mutex
	contexts    []context.Context
	handlers    []Handler
	topics      []string
	subscribed  chan string
	closed      []string
	failAt      int
	failErr     error
	closeErrors map[string]error
}

func newFakeSubscriber() *fakeSubscriber {
	return &fakeSubscriber{subscribed: make(chan string, 8)}
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, topic string, handler Handler) (Subscription, error) {
	f.mu.Lock()
	call := len(f.topics) + 1
	if f.failAt == call {
		err := f.failErr
		f.mu.Unlock()
		return nil, err
	}
	f.contexts = append(f.contexts, ctx)
	f.handlers = append(f.handlers, handler)
	f.topics = append(f.topics, topic)
	f.mu.Unlock()
	f.subscribed <- topic
	return &fakeSubscription{owner: f, topic: topic}, nil
}

func (f *fakeSubscriber) snapshot() (contexts []context.Context, closed []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]context.Context(nil), f.contexts...), append([]string(nil), f.closed...)
}

type fakeSubscription struct {
	owner *fakeSubscriber
	topic string
	once  sync.Once
	err   error
}

func (s *fakeSubscription) Close(context.Context) error {
	s.once.Do(func() {
		s.owner.mu.Lock()
		defer s.owner.mu.Unlock()
		s.owner.closed = append(s.owner.closed, s.topic)
		s.err = s.owner.closeErrors[s.topic]
	})
	return s.err
}

func TestServerLifecycle(t *testing.T) {
	subscriber := newFakeSubscriber()
	server := NewServer(subscriber)
	for _, topic := range []string{"accounts.created", "accounts.deleted"} {
		if err := server.Handle(topic, func(context.Context, any) (any, error) { return nil, nil }); err != nil {
			t.Fatalf("Handle(%q): %v", topic, err)
		}
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(context.Background()) }()
	waitTopics(t, subscriber.subscribed, 2)

	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}

	contexts, closed := subscriber.snapshot()
	if want := []string{"accounts.deleted", "accounts.created"}; !reflect.DeepEqual(closed, want) {
		t.Errorf("close order = %v, want %v", closed, want)
	}
	for i, ctx := range contexts {
		select {
		case <-ctx.Done():
		default:
			t.Errorf("subscription context %d was not canceled", i)
		}
	}
	if err := server.Handle("late", func(context.Context, any) (any, error) { return nil, nil }); !errors.Is(err, ErrStopped) {
		t.Errorf("late Handle error = %v, want ErrStopped", err)
	}
	if err := server.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Errorf("second Start error = %v, want ErrStopped", err)
	}
}

func TestServerPassesConcreteDestinationThroughMiddleware(t *testing.T) {
	subscriber := newFakeSubscriber()
	var gotDestination string
	var gotMessage *Message
	server := NewServer(subscriber, WithMiddleware(func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			if ctx == nil {
				t.Fatal("middleware received nil context")
			}
			return next(ctx, req)
		}
	}))
	if err := server.Handle("accounts.*", func(ctx context.Context, req any) (any, error) {
		if tr, ok := transport.FromServerContext(ctx); ok {
			gotDestination = tr.Operation()
		}
		gotMessage, _ = req.(*Message)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(context.Background()) }()
	waitTopics(t, subscriber.subscribed, 1)

	subscriber.mu.Lock()
	handler := subscriber.handlers[0]
	subscriber.mu.Unlock()
	wantMessage := New([]byte("payload"))
	if err := handler(context.Background(), "accounts.created", wantMessage); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if gotDestination != "accounts.created" {
		t.Errorf("destination = %q, want accounts.created", gotDestination)
	}
	if gotMessage != wantMessage {
		t.Errorf("message pointer = %p, want %p", gotMessage, wantMessage)
	}

	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-startErr; err != nil {
		t.Fatal(err)
	}
}

func TestServerSubscriptionFailureRollsBack(t *testing.T) {
	wantErr := errors.New("broker unavailable")
	subscriber := newFakeSubscriber()
	subscriber.failAt = 2
	subscriber.failErr = wantErr
	server := NewServer(subscriber)
	for _, topic := range []string{"first", "second"} {
		if err := server.Handle(topic, func(context.Context, any) (any, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	}

	err := server.Start(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v, want %v", err, wantErr)
	}
	_, closed := subscriber.snapshot()
	if want := []string{"first"}; !reflect.DeepEqual(closed, want) {
		t.Errorf("closed = %v, want %v", closed, want)
	}
}

func TestServerJoinsCloseErrors(t *testing.T) {
	firstErr := errors.New("close first")
	secondErr := errors.New("close second")
	subscriber := newFakeSubscriber()
	subscriber.closeErrors = map[string]error{"first": firstErr, "second": secondErr}
	server := NewServer(subscriber)
	for _, topic := range []string{"first", "second"} {
		if err := server.Handle(topic, func(context.Context, any) (any, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(context.Background()) }()
	waitTopics(t, subscriber.subscribed, 2)
	err := server.Stop(context.Background())
	if !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Stop error = %v, want both close errors", err)
	}
	if err := <-startErr; !errors.Is(err, firstErr) || !errors.Is(err, secondErr) {
		t.Fatalf("Start error = %v, want both close errors", err)
	}
}

func TestServerParentCancellationClosesSubscriptions(t *testing.T) {
	subscriber := newFakeSubscriber()
	server := NewServer(subscriber, ShutdownTimeout(time.Second))
	if err := server.Handle("events", func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(ctx) }()
	waitTopics(t, subscriber.subscribed, 1)
	cancel()
	select {
	case err := <-startErr:
		if err != nil {
			t.Fatalf("Start after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return after parent cancellation")
	}
	_, closed := subscriber.snapshot()
	if want := []string{"events"}; !reflect.DeepEqual(closed, want) {
		t.Errorf("closed = %v, want %v", closed, want)
	}
}

func TestServerValidatesConfigurationAndFreezesMiddleware(t *testing.T) {
	if err := NewServer(nil).Start(context.Background()); !errors.Is(err, ErrNilSubscriber) {
		t.Errorf("nil subscriber error = %v", err)
	}
	if err := NewServer(newFakeSubscriber()).Start(context.Background()); !errors.Is(err, ErrNoBindings) {
		t.Errorf("empty server error = %v", err)
	}

	subscriber := newFakeSubscriber()
	server := NewServer(subscriber)
	if err := server.Handle("", func(context.Context, any) (any, error) { return nil, nil }); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("empty topic error = %v", err)
	}
	if err := server.Handle("events", nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("nil handler error = %v", err)
	}
	if err := server.Handle("events", func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(context.Background()) }()
	waitTopics(t, subscriber.subscribed, 1)
	if err := server.Use(func(next middleware.UnaryHandler) middleware.UnaryHandler { return next }); !errors.Is(err, ErrAlreadyStarted) {
		t.Errorf("Use after Start error = %v, want ErrAlreadyStarted", err)
	}
	if err := server.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-startErr; err != nil {
		t.Fatal(err)
	}
}

func waitTopics(t *testing.T, topics <-chan string, count int) {
	t.Helper()
	for range count {
		select {
		case <-topics:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for subscription")
		}
	}
}
