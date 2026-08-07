package transport

import (
	"context"
	"net/url"

	// init encoding
	_ "github.com/sylphylabs/forge/encoding/form"
	_ "github.com/sylphylabs/forge/encoding/json"
	_ "github.com/sylphylabs/forge/encoding/proto"
	_ "github.com/sylphylabs/forge/encoding/protojson"
	_ "github.com/sylphylabs/forge/encoding/xml"
	_ "github.com/sylphylabs/forge/encoding/yaml"
)

// Server is transport server.
type Server interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Endpointer is registry endpoint.
type Endpointer interface {
	Endpoint() (*url.URL, error)
}

// Header is the storage medium used by a Header.
type Header interface {
	Get(key string) string
	Set(key string, value string)
	Add(key string, value string)
	Keys() []string
	Values(key string) []string
}

// Transporter carries what middleware needs to know about the call in flight.
//
// Its methods describe what every transport has, not the shape of a
// request/response exchange. A transport that has no meaningful reply header —
// a message queue, a bidirectional stream — implements this interface and, if
// it does expose one, [ReplyHeaderer] as well.
type Transporter interface {
	// Kind returns the transport kind, such as KindHTTP or KindGRPC. The set
	// is open: a transport outside this module may declare its own Kind.
	Kind() Kind
	// Endpoint returns the server or client endpoint.
	// Server Transport: grpc://127.0.0.1:9000
	// Client Transport: discovery:///provider-demo
	Endpoint() string
	// Operation identifies the call in flight. The format belongs to the
	// transport: gRPC reports the protobuf method selector
	// (/helloworld.Greeter/SayHello), a message transport reports the
	// destination (orders.created), a JSON-RPC transport reports the method
	// name.
	//
	// Callers MUST treat the value as opaque and MUST NOT parse it. It is
	// meant for labeling and keying — span names, rate-limit dimensions,
	// selector matching, log fields. Dispatch on Kind and read the concrete
	// transport type when structure is required.
	Operation() string
	// RequestHeader returns the transport request header.
	// http: http.Header
	// grpc: metadata.MD
	RequestHeader() Header
}

// ReplyHeaderer is implemented by transports that expose a mutable reply
// header. Request/response transports do; message and stream transports need
// not. Consumers type-assert for it.
type ReplyHeaderer interface {
	// ReplyHeader returns the transport reply header. Only valid for a server
	// transport.
	// http: http.Header
	// grpc: metadata.MD
	ReplyHeader() Header
}

// Kind defines the type of Transport. It is an open type: a transport outside
// this module may declare its own constant rather than extending the set
// below.
type Kind string

func (k Kind) String() string { return string(k) }

// Defines a set of transport kind
const (
	KindGRPC Kind = "grpc"
	KindHTTP Kind = "http"
)

type (
	serverTransportKey struct{}
	clientTransportKey struct{}
)

// NewServerContext returns a new Context that carries value.
func NewServerContext(ctx context.Context, tr Transporter) context.Context {
	return context.WithValue(ctx, serverTransportKey{}, tr)
}

// FromServerContext returns the Transport value stored in ctx, if any.
func FromServerContext(ctx context.Context) (tr Transporter, ok bool) {
	tr, ok = ctx.Value(serverTransportKey{}).(Transporter)
	return
}

// NewClientContext returns a new Context that carries value.
func NewClientContext(ctx context.Context, tr Transporter) context.Context {
	return context.WithValue(ctx, clientTransportKey{}, tr)
}

// FromClientContext returns the Transport value stored in ctx, if any.
func FromClientContext(ctx context.Context) (tr Transporter, ok bool) {
	tr, ok = ctx.Value(clientTransportKey{}).(Transporter)
	return
}
