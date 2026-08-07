package message

import (
	"context"

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

// withTransportContext wraps next so every delivery carries a Transport. It
// runs outside the message middleware chain, so that chain also sees it.
func withTransportContext(endpoint string, next Handler) Handler {
	return func(ctx context.Context, destination string, msg *Message) error {
		return next(withTransport(ctx, endpoint, destination, msg), destination, msg)
	}
}
