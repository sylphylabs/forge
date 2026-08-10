package message

import (
	"context"
	"testing"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func TestTransportImplementsTransporter(t *testing.T) {
	msg := New([]byte("body"))
	msg.Headers.Set("x-md-global-tenant", "acme")
	ctx := withTransport(t.Context(), "nats://127.0.0.1:4222", "orders.created", msg)

	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		t.Fatal("transport missing from context")
	}

	if got := tr.Kind(); got != KindMessage {
		t.Errorf("Kind() = %v, want %v", got, KindMessage)
	}
	if got := tr.Endpoint(); got != "nats://127.0.0.1:4222" {
		t.Errorf("Endpoint() = %q, want %q", got, "nats://127.0.0.1:4222")
	}
	if got := tr.Operation(); got != "orders.created" {
		t.Errorf("Operation() = %q, want the delivering destination", got)
	}
	if got := tr.RequestHeader().Get("x-md-global-tenant"); got != "acme" {
		t.Errorf("RequestHeader() tenant = %q, want %q", got, "acme")
	}
}

// A message delivery has no reply header, so the transport must not claim one.
func TestTransportIsNotReplyHeaderer(t *testing.T) {
	var tr transport.Transporter = &Transport{}
	if _, ok := tr.(transport.ReplyHeaderer); ok {
		t.Error("message Transport must not implement transport.ReplyHeaderer")
	}
}

func TestTransportHandlesMessageWithoutHeaders(t *testing.T) {
	ctx := withTransport(t.Context(), "", "orders.created", &Message{})
	tr, _ := transport.FromServerContext(ctx)

	if got := tr.RequestHeader().Get("absent"); got != "" {
		t.Errorf("RequestHeader() = %q, want empty", got)
	}
	if keys := tr.RequestHeader().Keys(); len(keys) != 0 {
		t.Errorf("Keys() = %v, want empty", keys)
	}
}

func TestTransportHandlesNilMessage(t *testing.T) {
	ctx := withTransport(t.Context(), "", "orders.created", nil)
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		t.Fatal("transport missing from context")
	}
	if got := tr.RequestHeader().Get("absent"); got != "" {
		t.Errorf("RequestHeader() = %q, want empty", got)
	}
}

func TestHeaderCarrierRoundTrip(t *testing.T) {
	msg := New([]byte("body"))
	ctx := withTransport(t.Context(), "", "orders.created", msg)
	tr, _ := transport.FromServerContext(ctx)
	header := tr.RequestHeader()

	header.Set("k", "v1")
	header.Add("k", "v2")

	if got := header.Get("k"); got != "v1" {
		t.Errorf("Get() = %q, want the first value %q", got, "v1")
	}
	if got := header.Values("k"); len(got) != 2 {
		t.Errorf("Values() = %v, want two values", got)
	}
	if got := header.Keys(); len(got) != 1 || got[0] != "k" {
		t.Errorf("Keys() = %v, want [k]", got)
	}
}

// The point of implementing Transporter: a non-RPC transport now reaches
// framework middleware that reads transport.Transporter.
func TestServerDeliveryCarriesTransport(t *testing.T) {
	var (
		gotKind      transport.Kind
		gotOperation string
		gotEndpoint  string
	)

	sub := newRecordingSubscriber()
	srv := NewServer(sub, Endpoint("nats://127.0.0.1:4222"))
	if err := srv.Handle("orders.*", func(ctx context.Context, _ any) (any, error) {
		if tr, ok := transport.FromServerContext(ctx); ok {
			gotKind = tr.Kind()
			gotOperation = tr.Operation()
			gotEndpoint = tr.Endpoint()
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	handler := sub.wait(t)
	// A wildcard subscription must report the concrete delivering destination.
	if err := handler(t.Context(), "orders.created", New([]byte("body"))); err != nil {
		t.Fatal(err)
	}

	if gotKind != KindMessage {
		t.Errorf("Kind() = %v, want %v", gotKind, KindMessage)
	}
	if gotOperation != "orders.created" {
		t.Errorf("Operation() = %q, want the concrete destination", gotOperation)
	}
	if gotEndpoint != "nats://127.0.0.1:4222" {
		t.Errorf("Endpoint() = %q, want the configured endpoint", gotEndpoint)
	}
}

// The transport must also be visible to the message middleware chain.
func TestMessageMiddlewareSeesTransport(t *testing.T) {
	var seen bool

	sub := newRecordingSubscriber()
	srv := NewServer(sub, WithMiddleware(func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			_, seen = transport.FromServerContext(ctx)
			return next(ctx, req)
		}
	}))
	if err := srv.Handle("orders.created", func(context.Context, any) (any, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = srv.Start(ctx) }()

	handler := sub.wait(t)
	if err := handler(t.Context(), "orders.created", New([]byte("body"))); err != nil {
		t.Fatal(err)
	}

	if !seen {
		t.Error("message middleware did not see the transport")
	}
}

// recordingSubscriber captures the handler the server registers.
type recordingSubscriber struct {
	handlers chan Handler
}

func newRecordingSubscriber() *recordingSubscriber {
	return &recordingSubscriber{handlers: make(chan Handler, 1)}
}

func (s *recordingSubscriber) Subscribe(_ context.Context, _ string, h Handler) (Subscription, error) {
	s.handlers <- h
	return noopSubscription{}, nil
}

func (s *recordingSubscriber) wait(t *testing.T) Handler {
	t.Helper()
	select {
	case h := <-s.handlers:
		return h
	case <-t.Context().Done():
		t.Fatal("subscription was never registered")
		return nil
	}
}

type noopSubscription struct{}

func (noopSubscription) Close(context.Context) error { return nil }
