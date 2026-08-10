package mqtt

import (
	"context"
	"errors"
	"testing"

	"github.com/eclipse/paho.golang/paho"

	"github.com/sylphylabs/forge/transport/message"
)

func TestTopicFilterMatching(t *testing.T) {
	cases := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"sport/tennis/player1", "sport/tennis/player1", true},
		{"sport/tennis/player1", "sport/tennis/player2", false},
		{"sport/tennis/+", "sport/tennis/player1", true},
		{"sport/tennis/+", "sport/tennis/player1/score", false},
		{"sport/+", "sport/", true},
		{"sport/+", "sport", false},
		{"+/tennis/#", "sport/tennis/player1/score", true},
		{"sport/#", "sport/tennis/player1", true},
		// `#` matches the parent level itself, per MQTT 5 section 4.7.1.2.
		{"sport/#", "sport", true},
		{"#", "any/topic/at/all", true},
		{"sport/tennis/#", "sport/badminton/player1", false},
		{"+", "sport", true},
		{"+", "sport/tennis", false},
	}
	for _, tc := range cases {
		if got := matches(tc.filter, tc.topic); got != tc.want {
			t.Errorf("matches(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

func TestRouteDeliversToEveryMatchingSubscription(t *testing.T) {
	r := newRouter(false)
	var topics []string
	record := func(_ context.Context, topic string, _ *message.Message) error {
		topics = append(topics, topic)
		return nil
	}
	r.add("sport/#", 1, record)
	r.add("sport/tennis/+", 1, record)
	r.add("other/#", 1, record)

	handled, err := r.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "sport/tennis/player1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Error("handled = false, want true for a matched delivery")
	}
	if len(topics) != 2 {
		t.Errorf("deliveries = %d, want 2 matching subscriptions", len(topics))
	}
}

func TestRouteGivesEachHandlerAnIndependentCopy(t *testing.T) {
	r := newRouter(false)
	mutate := func(_ context.Context, _ string, msg *message.Message) error {
		msg.Body[0] = 'X'
		return nil
	}
	var second []byte
	r.add("topic", 1, mutate)
	r.add("topic", 1, func(_ context.Context, _ string, msg *message.Message) error {
		second = msg.Body
		return nil
	})

	r.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "topic", Payload: []byte("ab")}})
	if string(second) != "ab" {
		t.Errorf("second handler saw %q, want an unmutated copy", second)
	}
}

func TestRouteReportsUnmatchedDelivery(t *testing.T) {
	r := newRouter(false)
	r.add("sport/#", 1, noopHandler)
	handled, err := r.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "news/today"}})
	if err != nil {
		t.Fatal(err)
	}
	if handled {
		t.Error("handled = true, want false for an unmatched delivery")
	}
}

func TestRouteIgnoresNilPacket(t *testing.T) {
	r := newRouter(false)
	if handled, err := r.route(paho.PublishReceived{}); handled || err != nil {
		t.Errorf("route(nil packet) = (%v, %v), want (false, nil)", handled, err)
	}
}

func TestRemoveStopsDelivery(t *testing.T) {
	r := newRouter(false)
	called := false
	entry := r.add("topic", 1, func(context.Context, string, *message.Message) error {
		called = true
		return nil
	})
	r.remove(entry)
	r.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "topic"}})
	if called {
		t.Error("handler ran after remove")
	}
}

func TestRemoveKeepsSiblingSubscriptionToSameFilter(t *testing.T) {
	r := newRouter(false)
	calls := 0
	count := func(context.Context, string, *message.Message) error {
		calls++
		return nil
	}
	first := r.add("topic", 1, count)
	r.add("topic", 1, count)
	r.remove(first)

	r.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "topic"}})
	if calls != 1 {
		t.Errorf("calls = %d, want 1 surviving subscription", calls)
	}
}

// The acknowledgement decision is the adapter's translation of the Forge
// handler error contract into MQTT, where no negative acknowledgement exists.
// Client is nil in these deliveries, so ack is a no-op and the test asserts
// the decision itself rather than the wire effect.
func TestAckDecisionFollowsHandlerOutcome(t *testing.T) {
	cases := []struct {
		name       string
		ackOnError bool
		handlerErr error
		want       bool
	}{
		{name: "success acknowledges", want: true},
		{name: "failure withholds acknowledgement", handlerErr: errors.New("boom"), want: false},
		{name: "failure acknowledges when configured", ackOnError: true, handlerErr: errors.New("boom"), want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRouter(tc.ackOnError)
			r.add("topic", 1, func(context.Context, string, *message.Message) error { return tc.handlerErr })
			if got := r.shouldAck(paho.PublishReceived{Packet: &paho.Publish{Topic: "topic"}}); got != tc.want {
				t.Errorf("shouldAck = %v, want %v", got, tc.want)
			}
		})
	}
}

// An unmatched delivery must still be acknowledged: manual acknowledgement is
// enabled connection-wide, so withholding it would stall the broker's
// in-flight window on a message no handler wants.
func TestUnmatchedDeliveryIsAcknowledged(t *testing.T) {
	r := newRouter(false)
	r.add("sport/#", 1, noopHandler)
	if !r.shouldAck(paho.PublishReceived{Packet: &paho.Publish{Topic: "news/today"}}) {
		t.Error("shouldAck = false, want true for an unmatched delivery")
	}
}
