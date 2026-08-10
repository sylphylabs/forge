package mqtt

import (
	"context"
	"testing"
	"time"

	"github.com/sylphylabs/forge/transport/message"
)

// Subscriptions must survive a dropped connection. autopaho reconnects, and
// the adapter re-sends SUBSCRIBE from OnConnectionUp, so delivery resumes
// without the application re-registering anything.
func TestIntegrationResubscribeAfterReconnect(t *testing.T) {
	client := integrationClient(t)
	topic := uniqueTopic(t)
	delivered := make(chan string, 4)

	sub, err := client.Subscribe(t.Context(), topic, func(_ context.Context, _ string, msg *message.Message) error {
		delivered <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(t.Context())

	if err := client.Publish(t.Context(), topic, message.New([]byte("before"))); err != nil {
		t.Fatal(err)
	}
	if got := awaitDelivery(t, delivered); got != "before" {
		t.Fatalf("delivery = %q, want before", got)
	}

	// Terminating the connection exercises the reconnect path without
	// restarting the broker, which would disturb other tests.
	cm, ok := client.conn.(interface{ TerminateConnectionForTest() })
	if !ok {
		t.Fatal("connection does not support test termination")
	}
	cm.TerminateConnectionForTest()

	// A separate publisher avoids racing the reconnecting client's own
	// publish path.
	publisher := integrationClient(t)
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if err := publisher.Publish(t.Context(), topic, message.New([]byte("after"))); err != nil {
			t.Fatalf("publish after reconnect: %v", err)
		}
		select {
		case got := <-delivered:
			if got != "after" {
				t.Fatalf("delivery = %q, want after", got)
			}
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatal("subscription did not resume after reconnect")
}

func awaitDelivery(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delivery")
		return ""
	}
}
