package tracing

import (
	"context"
	"net"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/stats"
)

type ctxKey string

const testKey ctxKey = "MY_TEST_KEY"

type recordingSpan struct {
	trace.Span
	spanContext trace.SpanContext
	attributes  []attribute.KeyValue
}

func (s *recordingSpan) SpanContext() trace.SpanContext { return s.spanContext }
func (s *recordingSpan) SetAttributes(attrs ...attribute.KeyValue) {
	s.attributes = append(s.attributes, attrs...)
}

func validSpanContext() trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: trace.TraceID{1},
		SpanID:  trace.SpanID{1},
	})
}

func TestClientHandleConn(t *testing.T) {
	(&ClientHandler{}).HandleConn(t.Context(), nil)
}

func TestClientTagConn(t *testing.T) {
	ctx := context.WithValue(t.Context(), testKey, 123)
	if got := (&ClientHandler{}).TagConn(ctx, nil).Value(testKey); got != 123 {
		t.Fatalf("context value = %v, want 123", got)
	}
}

func TestClientTagRPC(t *testing.T) {
	ctx := context.WithValue(t.Context(), testKey, 123)
	if got := (&ClientHandler{}).TagRPC(ctx, nil).Value(testKey); got != 123 {
		t.Fatalf("context value = %v, want 123", got)
	}
}

func TestClientHandleRPCAttributes(t *testing.T) {
	tests := []struct {
		name      string
		stats     stats.RPCStats
		withPeer  bool
		spanCtx   trace.SpanContext
		wantAttrs []attribute.KeyValue
	}{
		{name: "non header stats", stats: nil, withPeer: true, spanCtx: validSpanContext()},
		{name: "missing peer", stats: &stats.OutHeader{}, spanCtx: validSpanContext()},
		{name: "invalid span", stats: &stats.OutHeader{}, withPeer: true},
		{
			name:     "valid peer",
			stats:    &stats.OutHeader{},
			withPeer: true,
			spanCtx:  validSpanContext(),
			wantAttrs: []attribute.KeyValue{
				semconv.NetworkPeerAddress("1.1.1.1"),
				semconv.NetworkPeerPort(8080),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			span := &recordingSpan{Span: noop.Span{}, spanContext: test.spanCtx}
			ctx := trace.ContextWithSpan(t.Context(), span)
			if test.withPeer {
				ctx = peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP("1.1.1.1"), Port: 8080}})
			}
			(&ClientHandler{}).HandleRPC(ctx, test.stats)
			if !attributeSetsEqual(span.attributes, test.wantAttrs) {
				t.Fatalf("attributes = %v, want %v", span.attributes, test.wantAttrs)
			}
		})
	}
}

func attributeSetsEqual(got, want []attribute.KeyValue) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
