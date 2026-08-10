package orderevents

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/transport/message"
)

// recordingSubscriber captures the topics a generated registration binds and
// keeps their handlers callable, so a test can deliver a message without a
// broker.
type recordingSubscriber struct {
	mu       sync.Mutex
	topics   []string
	handlers map[string]message.Handler
}

func newRecordingSubscriber() *recordingSubscriber {
	return &recordingSubscriber{handlers: make(map[string]message.Handler)}
}

func (r *recordingSubscriber) Subscribe(_ context.Context, topic string, handler message.Handler) (message.Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.topics = append(r.topics, topic)
	r.handlers[topic] = handler
	return noopSubscription{}, nil
}

func (r *recordingSubscriber) handler(topic string) (message.Handler, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	handler, ok := r.handlers[topic]
	return handler, ok
}

func (r *recordingSubscriber) boundTopics() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.topics)
}

type noopSubscription struct{}

func (noopSubscription) Close(context.Context) error { return nil }

// recordingServer records the decoded requests handed to it by the generated
// handlers, along with the destination each delivery reported.
type recordingServer struct {
	mu           sync.Mutex
	created      []*OrderCreated
	shipped      []*OrderShipped
	destinations []string
	err          error
}

func (s *recordingServer) OnOrderCreated(ctx context.Context, req *OrderCreated) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.created = append(s.created, req)
	s.record(ctx)
	return s.err
}

func (s *recordingServer) OnOrderShipped(ctx context.Context, req *OrderShipped) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shipped = append(s.shipped, req)
	s.record(ctx)
	return s.err
}

// record must be called with s.mu held.
//
// The destination comes from the transport in context, the same place an HTTP or
// gRPC handler reads its operation, rather than from an accessor generated per
// service.
func (s *recordingServer) record(ctx context.Context) {
	destination, _ := message.DestinationFromServerContext(ctx)
	s.destinations = append(s.destinations, destination)
}

// start registers srv on a message.Server bound to a recording subscriber and
// runs the server until the test ends.
func start(t *testing.T, srv OrderEventsMessageServer, opts ...OrderEventsMessageRegisterOption) *recordingSubscriber {
	t.Helper()
	subscriber := newRecordingSubscriber()
	server := message.NewServer(subscriber)
	if err := RegisterOrderEventsMessageServer(server, srv, opts...); err != nil {
		t.Fatalf("RegisterOrderEventsMessageServer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	// The service declares two subscribe-annotated methods, so Start binds two
	// subscriptions before it blocks.
	waitForTopics(t, subscriber, 2)
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Start() error = %v", err)
		}
	})
	return subscriber
}

func waitForTopics(t *testing.T, subscriber *recordingSubscriber, want int) {
	t.Helper()
	for range 10_000 {
		if len(subscriber.boundTopics()) >= want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("bound topics = %v, want %d", subscriber.boundTopics(), want)
}

func TestRegisterBindsDeclaredDestinations(t *testing.T) {
	subscriber := start(t, new(recordingServer))
	got := subscriber.boundTopics()
	slices.Sort(got)
	want := []string{"order.created", "order.shipped"}
	if !slices.Equal(got, want) {
		t.Fatalf("bound topics = %v, want %v", got, want)
	}
}

func TestRegisterOverridesDestination(t *testing.T) {
	subscriber := start(t, new(recordingServer),
		WithOrderEventsMessageDestination("OnOrderCreated", "staging.orders.created"),
	)
	got := subscriber.boundTopics()
	slices.Sort(got)
	// The replacement is absolute: it does not keep the declared destination.
	want := []string{"order.shipped", "staging.orders.created"}
	if !slices.Equal(got, want) {
		t.Fatalf("bound topics = %v, want %v", got, want)
	}
}

func TestRegisterPrefixesDestinations(t *testing.T) {
	subscriber := start(t, new(recordingServer),
		WithOrderEventsMessageDestinationPrefix("staging."),
	)
	got := subscriber.boundTopics()
	slices.Sort(got)
	want := []string{"staging.order.created", "staging.order.shipped"}
	if !slices.Equal(got, want) {
		t.Fatalf("bound topics = %v, want %v", got, want)
	}
}

func TestRegisterDestinationOverrideWinsOverPrefix(t *testing.T) {
	subscriber := start(t, new(recordingServer),
		WithOrderEventsMessageDestinationPrefix("staging."),
		WithOrderEventsMessageDestination("OnOrderCreated", "legacy.created"),
	)
	got := subscriber.boundTopics()
	slices.Sort(got)
	want := []string{"legacy.created", "staging.order.shipped"}
	if !slices.Equal(got, want) {
		t.Fatalf("bound topics = %v, want %v", got, want)
	}
}

func TestHandlerDecodesRequestAndExposesDelivery(t *testing.T) {
	server := new(recordingServer)
	subscriber := start(t, server, WithOrderEventsMessageDestinationPrefix("staging."))

	handler, ok := subscriber.handler("staging.order.created")
	if !ok {
		t.Fatalf("no handler for staging.order.created; bound %v", subscriber.boundTopics())
	}
	body, err := proto.Marshal(&OrderCreated{Id: "42", Customer: "ada"})
	if err != nil {
		t.Fatal(err)
	}
	msg := message.New(body)
	msg.ID = "delivery-1"
	// A wildcard subscription can deliver a destination other than the bound one.
	if err := handler(t.Context(), "staging.order.created.eu", msg); err != nil {
		t.Fatalf("handler() error = %v", err)
	}

	if len(server.created) != 1 {
		t.Fatalf("decoded requests = %d, want 1", len(server.created))
	}
	if got := server.created[0].GetId(); got != "42" {
		t.Errorf("id = %q, want %q", got, "42")
	}
	if got := server.created[0].GetCustomer(); got != "ada" {
		t.Errorf("customer = %q, want %q", got, "ada")
	}
	if got := server.destinations[0]; got != "staging.order.created.eu" {
		t.Errorf("delivered destination = %q, want %q", got, "staging.order.created.eu")
	}
}

func TestHandlerPropagatesServerError(t *testing.T) {
	sentinel := errors.New("handler failed")
	server := &recordingServer{err: sentinel}
	subscriber := start(t, server)

	handler, ok := subscriber.handler("order.shipped")
	if !ok {
		t.Fatalf("no handler for order.shipped; bound %v", subscriber.boundTopics())
	}
	body, err := proto.Marshal(&OrderShipped{Id: "42", Carrier: "dhl"})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler(t.Context(), "order.shipped", message.New(body)); !errors.Is(err, sentinel) {
		t.Fatalf("handler() error = %v, want %v", err, sentinel)
	}
}

func TestHandlerReportsDecodeFailure(t *testing.T) {
	server := new(recordingServer)
	subscriber := start(t, server)

	handler, ok := subscriber.handler("order.created")
	if !ok {
		t.Fatalf("no handler for order.created; bound %v", subscriber.boundTopics())
	}
	// A field-number/wire-type pair that cannot decode into OrderCreated.
	if err := handler(t.Context(), "order.created", message.New([]byte{0xff, 0xff})); err == nil {
		t.Fatal("handler() error = nil for an undecodable body")
	}
	if len(server.created) != 0 {
		t.Fatalf("decoded requests = %d, want 0", len(server.created))
	}
}

// The declared destinations stay available as constants so callers can build
// their own override maps from the contract.
func TestDeclaredDestinationConstants(t *testing.T) {
	if DestinationOrderEventsOnOrderCreated != "order.created" {
		t.Errorf("DestinationOrderEventsOnOrderCreated = %q", DestinationOrderEventsOnOrderCreated)
	}
	if DestinationOrderEventsOnOrderShipped != "order.shipped" {
		t.Errorf("DestinationOrderEventsOnOrderShipped = %q", DestinationOrderEventsOnOrderShipped)
	}
	if OperationMessageOrderEventsOnOrderCreated != "/orderevents.OrderEvents/OnOrderCreated" {
		t.Errorf("OperationMessageOrderEventsOnOrderCreated = %q", OperationMessageOrderEventsOnOrderCreated)
	}
}

func TestDestinationFromServerContextReportsAbsence(t *testing.T) {
	if _, ok := message.DestinationFromServerContext(t.Context()); ok {
		t.Error("DestinationFromServerContext() reported a destination outside a delivery")
	}
}
