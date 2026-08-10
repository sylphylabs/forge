package metadata

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	grpcmd "google.golang.org/grpc/metadata"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func TestEncodeValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"printable ascii is untouched", "global-value", "global-value"},
		{"empty is untouched", "", ""},
		{"space is printable", "a b", "a b"},
		{"percent is escaped", "100%", "100%25"},
		{"non-ascii is escaped", "张三", "%E5%BC%A0%E4%B8%89"},
		{"latin-1 is escaped", "café", "caf%C3%A9"},
		{"newline is escaped", "a\nb", "a%0Ab"},
		{"nul is escaped", "a\x00b", "a%00b"},
		{"del is escaped", "a\x7fb", "a%7Fb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeValue(tt.value); got != tt.want {
				t.Errorf("encodeValue(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestDecodeValue(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"plain value is untouched", "global-value", "global-value"},
		{"escaped non-ascii decodes", "%E5%BC%A0%E4%B8%89", "张三"},
		{"escaped percent decodes", "100%25", "100%"},
		{"bare percent from legacy peer is kept", "100%", "100%"},
		{"invalid escape from legacy peer is kept", "%zz", "%zz"},
		{"lone percent from legacy peer is kept", "%", "%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := decodeValue(tt.value); got != tt.want {
				t.Errorf("decodeValue(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestEncodeValueRoundTrip(t *testing.T) {
	values := []string{
		"", "plain", "a b", "100%", "%", "%zz", "张三", "café",
		"a\nb", "a\x00b", "a\x7fb", "a/b", "a+b", "混合 mixed 100%",
	}
	for _, v := range values {
		encoded := encodeValue(v)
		for i := range len(encoded) {
			if c := encoded[i]; c < 0x20 || c > 0x7E {
				t.Errorf("encodeValue(%q) = %q contains non-printable byte %#x", v, encoded, c)
				break
			}
		}
		if got := decodeValue(encoded); got != v {
			t.Errorf("round trip of %q through %q gave %q", v, encoded, got)
		}
	}
}

// TestClientEncodesServerDecodes proves the middleware pair is symmetric: what
// the client writes to the header is what the server hands to the handler.
func TestClientEncodesServerDecodes(t *testing.T) {
	const key = "x-md-global-user"
	const value = "张三"

	hc := headerCarrier{}
	clientCtx := transport.NewClientContext(context.Background(), &testTransport{hc})
	clientMD := metadata.New()
	clientMD.Set(key, value)
	clientCtx = metadata.NewClientContext(clientCtx, clientMD)

	if _, err := Client()(func(ctx context.Context, req any) (any, error) {
		return req, nil
	})(clientCtx, "req"); err != nil {
		t.Fatal(err)
	}

	onWire := hc.Get(key)
	if onWire == value {
		t.Fatalf("client wrote %q to the header unescaped", onWire)
	}

	serverCtx := transport.NewServerContext(context.Background(), &testTransport{hc})
	var got string
	if _, err := Server()(func(ctx context.Context, req any) (any, error) {
		md, ok := metadata.FromServerContext(ctx)
		if !ok {
			t.Fatal("no metadata in server context")
		}
		got = md.Get(key)
		return req, nil
	})(serverCtx, "req"); err != nil {
		t.Fatal(err)
	}
	if got != value {
		t.Errorf("server observed %q, want %q", got, value)
	}
}

// TestServerAcceptsLegacyUnescapedValue covers a peer that predates encoding.
// Such a peer sends printable ASCII verbatim, and a value containing '%' must
// not be mangled by a decode it never asked for.
func TestServerAcceptsLegacyUnescapedValue(t *testing.T) {
	const key = "x-md-global-discount"
	for _, legacy := range []string{"plain", "100%", "%zz", "50%off"} {
		hc := headerCarrier{}
		hc.Set(key, legacy)
		ctx := transport.NewServerContext(context.Background(), &testTransport{hc})

		var got string
		if _, err := Server()(func(ctx context.Context, req any) (any, error) {
			md, _ := metadata.FromServerContext(ctx)
			got = md.Get(key)
			return req, nil
		})(ctx, "req"); err != nil {
			t.Fatal(err)
		}
		if got != legacy {
			t.Errorf("legacy value %q was read as %q", legacy, got)
		}
	}
}

// TestClientKeepsPrintableASCIIVerbatim is the other half of the skew contract:
// a peer that does not decode still reads an ordinary value correctly, because
// encoding only engages when the value could not travel as is.
func TestClientKeepsPrintableASCIIVerbatim(t *testing.T) {
	const key = "x-md-global-key"
	const value = "global-value"

	hc := headerCarrier{}
	ctx := transport.NewClientContext(context.Background(), &testTransport{hc})
	md := metadata.New()
	md.Set(key, value)
	ctx = metadata.NewClientContext(ctx, md)

	if _, err := Client()(func(ctx context.Context, req any) (any, error) {
		return req, nil
	})(ctx, "req"); err != nil {
		t.Fatal(err)
	}
	if got := hc.Get(key); got != value {
		t.Errorf("printable value was rewritten to %q, want %q", got, value)
	}
}

func TestServerStreamDecodesValues(t *testing.T) {
	const key = "x-md-global-user"
	const value = "张三"

	hc := headerCarrier{}
	hc.Set(key, encodeValue(value))
	ctx := transport.NewServerContext(context.Background(), &testTransport{hc})

	var got string
	err := ServerStream()(func(_ any, stream middleware.ServerStream) error {
		md, ok := metadata.FromServerContext(stream.Context())
		if !ok {
			t.Fatal("no metadata in stream context")
		}
		got = md.Get(key)
		return nil
	})(nil, &testStream{ctx: ctx})
	if err != nil {
		t.Fatal(err)
	}
	if got != value {
		t.Errorf("stream handler observed %q, want %q", got, value)
	}
}

// TestGRPCAcceptsEncodedMetadata is the end-to-end guard. gRPC admits only
// printable ASCII in a non-binary header and fails the call with an Internal
// error otherwise, so an unencoded value never reaches the server. The encoded
// form has to reach it and decode back to the original.
//
// The handler replies with no message, so a call that arrives ends in a
// cardinality violation reported by the client. That is the success signal
// here: the request was transmitted and served. What must not appear is the
// header rejection, which fails the call locally before any of that.
func TestGRPCAcceptsEncodedMetadata(t *testing.T) {
	const key = "x-md-global-user"
	const value = "张三"
	const headerRejection = "non-printable ASCII characters"

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	srv := grpc.NewServer(grpc.UnknownServiceHandler(func(_ any, stream grpc.ServerStream) error {
		var got string
		if md, ok := grpcmd.FromIncomingContext(stream.Context()); ok {
			if v := md.Get(key); len(v) > 0 {
				got = decodeValue(v[0])
			}
		}
		received <- got
		return nil
	}))
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	cc, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()

	invoke := func(headerValue string) error {
		ctx := grpcmd.AppendToOutgoingContext(context.Background(), key, headerValue)
		return cc.Invoke(ctx, "/probe.Service/Method", &emptyMessage{}, &emptyMessage{})
	}

	// Establish that the rejection this fix exists for is still real; without
	// it the encoded case below would prove nothing.
	rawErr := invoke(value)
	if rawErr == nil || !strings.Contains(rawErr.Error(), headerRejection) {
		t.Skipf("gRPC no longer rejects an unencoded value (%v); the escape is moot", rawErr)
	}

	if err := invoke(encodeValue(value)); err != nil &&
		strings.Contains(err.Error(), headerRejection) {
		t.Fatalf("encoded metadata was still rejected as a header: %v", err)
	}

	select {
	case got := <-received:
		if got != value {
			t.Errorf("server received %q, want %q", got, value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the encoded call never reached the server")
	}
}

// emptyMessage carries no fields, which is all a call needs when the
// unknown-service handler never inspects the body. Marshal and Unmarshal keep
// the gRPC codec from reaching for a descriptor this stub does not have.
type emptyMessage struct{}

func (*emptyMessage) Reset()         {}
func (*emptyMessage) String() string { return "" }
func (*emptyMessage) ProtoMessage()  {}

func (*emptyMessage) Marshal() ([]byte, error) { return nil, nil }
func (*emptyMessage) Unmarshal([]byte) error   { return nil }
