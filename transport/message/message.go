// Package message defines the protocol-neutral contract for asynchronous
// message transports. Broker-specific clients belong in optional modules.
package message

import (
	"context"
	"slices"

	"github.com/sylphylabs/forge/metadata"
)

// Message is the portable part of a delivered message.
//
// Body is the encoded payload. Broker-specific delivery state such as
// partition, offset, acknowledgement handles, and raw SDK messages must stay
// in an adapter rather than becoming part of this contract.
type Message struct {
	ID      string
	Key     string
	Headers metadata.Metadata
	Body    []byte
}

// New creates a message and takes a copy of body. This makes the caller's
// buffer safe to reuse after New returns.
func New(body []byte) *Message {
	return &Message{
		Headers: make(metadata.Metadata),
		Body:    slices.Clone(body),
	}
}

// Clone returns a deep copy of the portable message fields.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	return &Message{
		ID:      m.ID,
		Key:     m.Key,
		Headers: m.Headers.Clone(),
		Body:    slices.Clone(m.Body),
	}
}

// SetHeader sets a single-valued header. Header names are normalized in the
// same way as metadata used by HTTP and gRPC transports.
func (m *Message) SetHeader(key, value string) {
	if m == nil {
		return
	}
	if m.Headers == nil {
		m.Headers = make(metadata.Metadata)
	}
	m.Headers.Set(key, value)
}

// AddHeader appends a header value.
func (m *Message) AddHeader(key, value string) {
	if m == nil {
		return
	}
	if m.Headers == nil {
		m.Headers = make(metadata.Metadata)
	}
	m.Headers.Add(key, value)
}

// Header returns the first value for key.
func (m *Message) Header(key string) string {
	if m == nil {
		return ""
	}
	return m.Headers.Get(key)
}

// Publisher publishes encoded messages to a destination.
//
// The context covers the publish operation. Adapters must document whether a
// successful return means broker acknowledgement or only local enqueueing.
type Publisher interface {
	Publish(context.Context, string, *Message) error
}

// Subscriber creates subscriptions for delivered messages. The context is
// the subscription lifetime: cancellation must stop delivery and release the
// adapter's resources. Close remains available for bounded, explicit shutdown.
type Subscriber interface {
	Subscribe(context.Context, string, Handler) (Subscription, error)
}

// Subscription is one active destination binding.
type Subscription interface {
	Close(context.Context) error
}

// Handler processes one message. Destination is the concrete destination that
// delivered the message, which may differ from a wildcard subscription.
// Returning an error leaves acknowledgement and retry policy to the broker
// adapter; the core contract does not guess those semantics.
type Handler func(context.Context, string, *Message) error

// Middleware wraps a message handler.
type Middleware func(Handler) Handler

// Chain composes middleware in declaration order. Nil middleware is ignored.
func Chain(m ...Middleware) Middleware {
	return func(next Handler) Handler {
		for i := len(m) - 1; i >= 0; i-- {
			if m[i] != nil {
				next = m[i](next)
			}
		}
		return next
	}
}
