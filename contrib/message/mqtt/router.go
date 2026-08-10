package mqtt

import (
	"context"
	"strings"
	"sync"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/sylphylabs/forge/transport/message"
)

// routeEntry is one registered topic filter. It is identified by pointer so
// that two subscriptions to the same filter remain independent.
type routeEntry struct {
	filter  string
	qos     byte
	handler message.Handler
}

// router dispatches inbound PUBLISH packets to matching subscriptions and owns
// the acknowledgement decision.
//
// A single MQTT connection multiplexes every subscription, so the broker gives
// no per-subscription callback: matching the delivered topic against the
// registered filters is the adapter's job. paho's own router is not used
// because acknowledgement here depends on the aggregate handler outcome.
type router struct {
	ackOnError bool

	mu      sync.RWMutex
	entries []*routeEntry
}

func newRouter(ackOnError bool) *router {
	return &router{ackOnError: ackOnError}
}

func (r *router) add(filter string, qos byte, handler message.Handler) *routeEntry {
	entry := &routeEntry{filter: filter, qos: qos, handler: handler}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return entry
}

func (r *router) remove(entry *routeEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, candidate := range r.entries {
		if candidate == entry {
			r.entries = append(r.entries[:i], r.entries[i+1:]...)
			return
		}
	}
}

func (r *router) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = nil
}

func (r *router) snapshot() []*routeEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]*routeEntry(nil), r.entries...)
}

// route delivers one PUBLISH to every matching subscription and decides
// whether to acknowledge it.
//
// The message is decoded once and cloned per handler so that concurrent
// subscriptions cannot observe each other's mutations. The returned bool tells
// paho the packet was handled; the error is reported to the application
// through the client's ErrorHandler rather than here, because paho only logs
// it.
func (r *router) route(received paho.PublishReceived) (bool, error) {
	if received.Packet == nil {
		return false, nil
	}
	if r.shouldAck(received) {
		r.ack(received)
	}
	return r.matched(received.Packet.Topic), nil
}

// shouldAck runs the matching handlers and reports whether the delivery may be
// acknowledged. It is separate from route so the acknowledgement decision can
// be asserted without a paho client to ack against.
func (r *router) shouldAck(received paho.PublishReceived) bool {
	pub := received.Packet
	decoded := fromPublish(pub)
	matched := false
	failed := false
	for _, entry := range r.snapshot() {
		if !matches(entry.filter, pub.Topic) {
			continue
		}
		matched = true
		if err := entry.handler(context.Background(), pub.Topic, decoded.Clone()); err != nil {
			failed = true
		}
	}
	// Manual acknowledgement is enabled for the whole connection, so an
	// unmatched delivery still has to be acknowledged or it would stall the
	// broker's in-flight window on a message no handler wants.
	return !matched || !failed || r.ackOnError
}

func (r *router) matched(topic string) bool {
	for _, entry := range r.snapshot() {
		if matches(entry.filter, topic) {
			return true
		}
	}
	return false
}

// ack sends the PUBACK/PUBCOMP for a delivery. paho ignores the call for
// QoS 0, where the protocol has no acknowledgement packet.
func (r *router) ack(received paho.PublishReceived) {
	if received.Client == nil || received.Packet == nil {
		return
	}
	// An ack failure means the connection is already gone; the broker will
	// redeliver on session resumption, which is the same outcome as
	// deliberately withholding it.
	_ = received.Client.Ack(received.Packet)
}

// resubscribe restores subscriptions after a reconnect.
//
// It runs unconditionally rather than only when the broker reports no session
// present, because a broker may expire a session while the client believes it
// is durable. MQTT SUBSCRIBE is idempotent for a given filter, so re-sending
// it against a resumed session replaces the identical subscription.
func (r *router) resubscribe(cm *autopaho.ConnectionManager, _ *paho.Connack) {
	entries := r.snapshot()
	if len(entries) == 0 || cm == nil {
		return
	}
	subs := make([]paho.SubscribeOptions, 0, len(entries))
	for _, entry := range entries {
		subs = append(subs, paho.SubscribeOptions{Topic: entry.filter, QoS: entry.qos})
	}
	// OnConnectionUp must not block, so the SUBSCRIBE is issued from its own
	// goroutine. Failures surface as missing deliveries until the next
	// reconnect; autopaho owns the retry loop.
	go func() {
		_, _ = cm.Subscribe(context.Background(), &paho.Subscribe{Subscriptions: subs})
	}()
}

// matches reports whether an MQTT topic filter matches a concrete topic name,
// implementing the `+` single-level and `#` multi-level wildcards.
//
// Filters beginning with `$` are not special-cased: the spec leaves
// `$`-prefixed topic protection to the broker, and the adapter only ever
// matches topics the broker already chose to deliver.
func matches(filter, topic string) bool {
	if filter == topic {
		return true
	}
	filterParts := strings.Split(filter, "/")
	topicParts := strings.Split(topic, "/")
	for i, part := range filterParts {
		if part == "#" {
			// `#` is only legal as the final level and matches the remainder,
			// including the parent level itself.
			return i == len(filterParts)-1
		}
		if i >= len(topicParts) {
			return false
		}
		if part != "+" && part != topicParts[i] {
			return false
		}
	}
	return len(filterParts) == len(topicParts)
}
