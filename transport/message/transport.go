package message

import (
	"context"

	"github.com/sylphylabs/forge/middleware"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport"
)

// KindMessage identifies the asynchronous message transport. It is declared
// here rather than in the transport package because transport.Kind is an open
// type; see docs/adr/0001.
const KindMessage transport.Kind = "message"

var _ transport.Transporter = (*Transport)(nil)

// Transport reports the message delivery in flight to middleware.
//
// It intentionally does not implement transport.ReplyHeaderer: a delivered
// message has no reply header. Request/reply, where an adapter supports it,
// is an adapter capability rather than a property of the envelope.
type Transport struct {
	endpoint    string
	destination string
	header      headerCarrier
}

// Kind returns KindMessage.
func (tr *Transport) Kind() transport.Kind { return KindMessage }

// Endpoint returns the broker endpoint the subscription is bound to.
func (tr *Transport) Endpoint() string { return tr.endpoint }

// Operation returns the concrete destination that delivered the message, which
// may differ from a wildcard subscription. It is the message transport's
// answer to "which call is this", and is opaque to callers.
func (tr *Transport) Operation() string { return tr.destination }

// RequestHeader returns the headers of the delivered message.
func (tr *Transport) RequestHeader() transport.Header { return tr.header }

// headerCarrier adapts metadata.Metadata to transport.Header.
type headerCarrier metadata.Metadata

func (h headerCarrier) Get(key string) string { return metadata.Metadata(h).Get(key) }

func (h headerCarrier) Set(key string, value string) { metadata.Metadata(h).Set(key, value) }

func (h headerCarrier) Add(key string, value string) { metadata.Metadata(h).Add(key, value) }

func (h headerCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

func (h headerCarrier) Values(key string) []string { return metadata.Metadata(h).Values(key) }

// withTransport puts a Transport for this delivery in ctx so that framework
// middleware can read it through transport.FromServerContext.
func withTransport(ctx context.Context, endpoint, destination string, msg *Message) context.Context {
	header := headerCarrier{}
	if msg != nil && msg.Headers != nil {
		header = headerCarrier(msg.Headers)
	}
	return transport.NewServerContext(ctx, &Transport{
		endpoint:    endpoint,
		destination: destination,
		header:      header,
	})
}

// DestinationFromServerContext returns the destination that delivered the
// message being handled, and reports whether one was present.
//
// A handler reads its destination here rather than from a parameter, the way an
// HTTP handler reads its request. Under a wildcard subscription this is the
// concrete destination, not the pattern that matched it.
func DestinationFromServerContext(ctx context.Context) (string, bool) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return "", false
	}
	return tr.Operation(), true
}

// deliver adapts an application handler to the shape an adapter subscribes
// with.
//
// It is the one place the destination moves from a parameter into the context:
// an adapter has the value before any context exists, while an application
// reads it from the [transport.Transporter] the way an HTTP or gRPC handler
// reads its operation. Running outside the middleware chain means that chain
// sees the Transport too.
//
// A message has no reply, so the unary handler's first return value is
// discarded. Middleware written for HTTP and gRPC passes that value through
// without inspecting it, which is what lets the same middleware serve all three
// transports.
func deliver(endpoint string, next middleware.UnaryHandler) Handler {
	return func(ctx context.Context, destination string, msg *Message) error {
		_, err := next(withTransport(ctx, endpoint, destination, msg), msg)
		return err
	}
}
