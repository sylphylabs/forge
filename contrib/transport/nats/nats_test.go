package nats

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	server "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	"github.com/openkratos/kratos/metadata"
	"github.com/openkratos/kratos/transport/message"
)

func TestPublishSubscribeWithHeaders(t *testing.T) {
	url := runNATSServer(t)
	client := newClient(t, url)
	received := make(chan *message.Message, 1)

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	sub, err := client.Subscribe(subCtx, "accounts.created", func(ctx context.Context, subject string, msg *message.Message) error {
		if subject != "accounts.created" {
			t.Errorf("subject = %q, want accounts.created", subject)
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
	defer sub.Close(t.Context())

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = "acct-1"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	if err := client.Publish(timeoutContext(t), "accounts.created", msg); err != nil {
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

func TestMessageServerLifecycleWithNATS(t *testing.T) {
	url := runNATSServer(t)
	client := newClient(t, url)
	ready := make(chan struct{})
	server := message.NewServer(&readySubscriber{Client: client, ready: ready})
	delivered := make(chan string, 1)
	if err := server.Handle("orders.created", func(ctx context.Context, subject string, msg *message.Message) error {
		delivered <- subject + ":" + string(msg.Body)
		return nil
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
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message server subscription")
	}
	if err := client.Publish(timeoutContext(t), "orders.created", message.New([]byte("ok"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != "orders.created:ok" {
			t.Fatalf("delivery = %q, want orders.created:ok", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

func TestSubscriptionCancellationStopsDelivery(t *testing.T) {
	url := runNATSServer(t)
	client := newClient(t, url)
	delivered := make(chan struct{}, 1)
	subCtx, cancel := context.WithCancel(t.Context())
	sub, err := client.Subscribe(subCtx, "cancel.me", func(context.Context, string, *message.Message) error {
		delivered <- struct{}{}
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
	inner := sub.(*subscription)
	if inner.sub.IsValid() {
		t.Fatal("NATS subscription remained valid after cancellation")
	}
	if len(delivered) != 0 {
		t.Fatal("handler ran after cancellation")
	}
}

func TestHandlerErrorReported(t *testing.T) {
	url := runNATSServer(t)
	wantErr := errors.New("handler failed")
	reported := make(chan error, 1)
	client, err := New(WithURL(url), WithErrorHandler(func(_ context.Context, subject string, msg *message.Message, err error) {
		if subject != "events" {
			t.Errorf("subject = %q, want events", subject)
		}
		if string(msg.Body) != "payload" {
			t.Errorf("body = %q, want payload", msg.Body)
		}
		reported <- err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	sub, err := client.Subscribe(t.Context(), "events", func(context.Context, string, *message.Message) error {
		return wantErr
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(t.Context())
	if err := client.Publish(timeoutContext(t), "events", message.New([]byte("payload"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-reported:
		if !errors.Is(got, wantErr) {
			t.Fatalf("reported error = %v, want %v", got, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for handler error")
	}
}

func TestRequestReplyIsAdapterSpecific(t *testing.T) {
	url := runNATSServer(t)
	client := newClient(t, url)
	responder, err := natsgo.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(responder.Close)
	_, err = responder.Subscribe("rpc.echo", func(req *natsgo.Msg) {
		resp := natsgo.NewMsg(req.Reply)
		resp.Data = append([]byte("reply:"), req.Data...)
		resp.Header.Set("TraceParent", req.Header.Get("traceparent"))
		if err := responder.PublishMsg(resp); err != nil {
			t.Errorf("reply publish: %v", err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := responder.FlushWithContext(timeoutContext(t)); err != nil {
		t.Fatal(err)
	}

	req := message.New([]byte("hello"))
	req.SetHeader("traceparent", "00-rpc")
	reply, err := client.Request(timeoutContext(t), "rpc.echo", req)
	if err != nil {
		t.Fatal(err)
	}
	if string(reply.Body) != "reply:hello" {
		t.Fatalf("reply body = %q, want reply:hello", reply.Body)
	}
	if got := reply.Header("traceparent"); got != "00-rpc" {
		t.Fatalf("traceparent = %q, want 00-rpc", got)
	}
}

func TestValidationAndOwnership(t *testing.T) {
	url := runNATSServer(t)
	owned := newClient(t, url)
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	if err := owned.Publish(timeoutContext(t), "closed", message.New(nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed Publish error = %v, want ErrClosed", err)
	}

	conn, err := natsgo.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	client, err := New(WithConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if conn.IsClosed() {
		t.Fatal("application-owned connection was closed")
	}
	conn.Close()

	if _, err := New(WithConn(nil)); !errors.Is(err, ErrNilConn) {
		t.Fatalf("nil conn New error = %v, want ErrNilConn", err)
	}
	if _, err := New(WithURL("")); !errors.Is(err, ErrEmptyURL) {
		t.Fatalf("empty URL New error = %v, want ErrEmptyURL", err)
	}
	if err := client.Publish(nil, "events", message.New(nil)); !errors.Is(err, ErrNilContext) {
		t.Fatalf("nil context Publish error = %v, want ErrNilContext", err)
	}
	if err := client.Publish(timeoutContext(t), "", message.New(nil)); !errors.Is(err, ErrEmptySubject) {
		t.Fatalf("empty subject Publish error = %v, want ErrEmptySubject", err)
	}
	if err := client.Publish(timeoutContext(t), "events", nil); !errors.Is(err, ErrNilMessage) {
		t.Fatalf("nil message Publish error = %v, want ErrNilMessage", err)
	}
	if _, err := client.Subscribe(t.Context(), "events", nil); !errors.Is(err, ErrNilHandler) {
		t.Fatalf("nil handler Subscribe error = %v, want ErrNilHandler", err)
	}
}

func newClient(t *testing.T, url string) *Client {
	t.Helper()
	client, err := New(WithURL(url))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return client
}

func runNATSServer(t *testing.T) string {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   -1,
		NoLog:  true,
		NoSigs: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(time.Second) {
		s.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		s.Shutdown()
		s.WaitForShutdown()
	})
	return fmt.Sprintf("nats://%s", s.Addr())
}

func timeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)
	return ctx
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

type readySubscriber struct {
	*Client
	ready chan struct{}
}

func (s *readySubscriber) Subscribe(ctx context.Context, subject string, handler message.Handler) (message.Subscription, error) {
	sub, err := s.Client.Subscribe(ctx, subject, handler)
	if err == nil {
		close(s.ready)
	}
	return sub, err
}
