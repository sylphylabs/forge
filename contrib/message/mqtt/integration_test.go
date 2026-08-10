package mqtt

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sylphylabs/forge/transport/message"
)

// brokerEnv names the environment variable holding the URL of a running MQTT 5
// broker, for example "mqtt://127.0.0.1:1883". Integration tests skip unless it
// is set, so the default `go test ./...` needs no broker.
const brokerEnv = "FORGE_MQTT_BROKER_URL"

func brokerURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv(brokerEnv)
	if url == "" {
		t.Skipf("set %s to run MQTT integration tests", brokerEnv)
	}
	return url
}

// integrationClient connects a client with a per-test client identifier so
// concurrent runs against a shared broker cannot resume each other's sessions.
func integrationClient(t *testing.T, opts ...Option) *Client {
	t.Helper()
	base := []Option{
		WithURL(brokerURL(t)),
		WithClientID(fmt.Sprintf("forge-test-%s-%d", t.Name(), time.Now().UnixNano())),
		WithPublishQoS(1),
		WithSubscribeQoS(1),
	}
	client, err := New(t.Context(), append(base, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Close(ctx); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return client
}

func uniqueTopic(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("forge/test/%d", time.Now().UnixNano())
}

func TestIntegrationPublishSubscribeRoundTrip(t *testing.T) {
	client := integrationClient(t)
	topic := uniqueTopic(t)
	received := make(chan *message.Message, 1)

	sub, err := client.Subscribe(t.Context(), topic, func(_ context.Context, delivered string, msg *message.Message) error {
		if delivered != topic {
			t.Errorf("topic = %q, want %q", delivered, topic)
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
	if err := client.Publish(t.Context(), topic, msg); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-received:
		if got.ID != "evt-1" {
			t.Errorf("ID = %q, want evt-1", got.ID)
		}
		if got.Key != "acct-1" {
			t.Errorf("Key = %q, want acct-1", got.Key)
		}
		if string(got.Body) != "created" {
			t.Errorf("Body = %q, want created", got.Body)
		}
		if got.Headers.Get("traceparent") != "00-abc" {
			t.Errorf("traceparent = %q, want 00-abc", got.Headers.Get("traceparent"))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

func TestIntegrationWildcardDeliversConcreteTopic(t *testing.T) {
	client := integrationClient(t)
	prefix := uniqueTopic(t)
	concrete := prefix + "/child/leaf"
	delivered := make(chan string, 1)

	sub, err := client.Subscribe(t.Context(), prefix+"/#", func(_ context.Context, topic string, _ *message.Message) error {
		delivered <- topic
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(t.Context())

	if err := client.Publish(t.Context(), concrete, message.New([]byte("ok"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != concrete {
			t.Errorf("topic = %q, want the concrete topic %q", got, concrete)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

// A handler error withholds the PUBACK, so the broker still owns the message
// and redelivers it when the session resumes. This is the only redelivery
// signal MQTT 5 offers a subscriber, and it requires a durable session.
//
// The resumed client deliberately does not subscribe again: a redelivery of an
// unacknowledged in-flight message arrives on the restored session by itself,
// so a delivery here cannot be explained by a fresh subscription.
func TestIntegrationHandlerErrorWithholdsAck(t *testing.T) {
	for _, tc := range []struct {
		name          string
		ackOnError    bool
		wantRedeliver bool
	}{
		{name: "withheld ack redelivers", wantRedeliver: true},
		// The inverse pins the behaviour down: acknowledging a failed handler
		// discards the message, so nothing is left to redeliver.
		{name: "acknowledged error discards", ackOnError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientID := fmt.Sprintf("forge-redeliver-%d", time.Now().UnixNano())
			topic := uniqueTopic(t)
			durable := []Option{
				WithURL(brokerURL(t)),
				WithClientID(clientID),
				WithPublishQoS(1),
				WithSubscribeQoS(1),
				WithCleanStart(false),
				WithSessionExpiry(300),
				WithAckOnError(tc.ackOnError),
				WithAckInterval(5 * time.Millisecond),
			}

			failing, err := New(t.Context(), durable...)
			if err != nil {
				t.Fatal(err)
			}
			seen := make(chan struct{}, 4)
			if _, err := failing.Subscribe(t.Context(), topic, func(context.Context, string, *message.Message) error {
				seen <- struct{}{}
				return errors.New("handler rejected the message")
			}); err != nil {
				t.Fatal(err)
			}

			if err := failing.Publish(t.Context(), topic, message.New([]byte("retry me"))); err != nil {
				t.Fatal(err)
			}
			select {
			case <-seen:
			case <-time.After(10 * time.Second):
				t.Fatal("timed out waiting for first delivery")
			}

			// Acknowledgements are flushed on a ticker, so closing immediately
			// would drop a pending ack and make both cases look identical.
			time.Sleep(100 * time.Millisecond)

			// Dropping the connection without unsubscribing keeps the
			// subscription in the retained session along with any
			// unacknowledged message.
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := failing.Close(closeCtx); err != nil {
				t.Logf("close client: %v", err)
			}
			cancel()

			resumed, err := New(t.Context(), durable...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = resumed.Close(ctx)
			})
			redelivered := make(chan struct{}, 1)
			resumed.router.add(topic, 1, func(context.Context, string, *message.Message) error {
				redelivered <- struct{}{}
				return nil
			})
			if err := resumed.conn.AwaitConnection(t.Context()); err != nil {
				t.Fatal(err)
			}

			select {
			case <-redelivered:
				if !tc.wantRedeliver {
					t.Fatal("acknowledged message was redelivered")
				}
			case <-time.After(10 * time.Second):
				if tc.wantRedeliver {
					t.Fatal("unacknowledged message was not redelivered on session resumption")
				}
			}
		})
	}
}

func TestIntegrationErrorHandlerObservesHandlerFailure(t *testing.T) {
	observed := make(chan error, 1)
	client := integrationClient(t, WithAckOnError(true), WithErrorHandler(
		func(_ context.Context, _ string, _ *message.Message, err error) {
			observed <- err
		}))
	topic := uniqueTopic(t)

	want := errors.New("boom")
	sub, err := client.Subscribe(t.Context(), topic, func(context.Context, string, *message.Message) error {
		return want
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(t.Context())

	if err := client.Publish(t.Context(), topic, message.New([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-observed:
		if !errors.Is(got, want) {
			t.Errorf("error = %v, want %v", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the error handler")
	}
}

func TestIntegrationMessageServerLifecycle(t *testing.T) {
	client := integrationClient(t)
	topic := uniqueTopic(t)
	server := message.NewServer(client)
	delivered := make(chan string, 1)
	if err := server.Handle(topic, func(_ context.Context, req any) (any, error) {
		msg, _ := req.(*message.Message)
		delivered <- string(msg.Body)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(t.Context()) }()
	waitForRoutes(t, client, 1)

	if err := client.Publish(t.Context(), topic, message.New([]byte("ok"))); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-delivered:
		if got != "ok" {
			t.Fatalf("delivery = %q, want ok", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}

	if err := server.Stop(t.Context()); err != nil && !errors.Is(err, message.ErrStopped) {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("Start: %v", err)
	}
}
