package metadata

import (
	"context"
	"testing"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func TestServerStreamPropagatesPrefixedHeaders(t *testing.T) {
	hc := headerCarrier{}
	hc.Set("x-md-global-tenant", "acme")
	hc.Set("x-md-local-trace", "abc")
	hc.Set("authorization", "secret")
	ctx := transport.NewServerContext(t.Context(), &testTransport{hc})

	var got metadata.Metadata
	next := ServerStream()(func(_ any, stream middleware.ServerStream) error {
		md, ok := metadata.FromServerContext(stream.Context())
		if !ok {
			t.Fatal("metadata missing from stream context")
		}
		got = md
		return nil
	})
	if err := next(nil, &testStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}

	if v := got.Get("x-md-global-tenant"); v != "acme" {
		t.Errorf("x-md-global-tenant = %q, want %q", v, "acme")
	}
	if v := got.Get("x-md-local-trace"); v != "abc" {
		t.Errorf("x-md-local-trace = %q, want %q", v, "abc")
	}
	if v := got.Get("authorization"); v != "" {
		t.Errorf("authorization = %q, want it not to be propagated", v)
	}
}

func TestServerStreamAddsConstants(t *testing.T) {
	ctx := transport.NewServerContext(t.Context(), &testTransport{headerCarrier{}})

	next := ServerStream(WithConstants(metadata.New(map[string][]string{
		"x-md-global-region": {"eu"},
	})))(func(_ any, stream middleware.ServerStream) error {
		md, _ := metadata.FromServerContext(stream.Context())
		if v := md.Get("x-md-global-region"); v != "eu" {
			t.Errorf("x-md-global-region = %q, want %q", v, "eu")
		}
		return nil
	})
	if err := next(nil, &testStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
}

func TestServerStreamCustomPrefix(t *testing.T) {
	hc := headerCarrier{}
	hc.Set("x-forge-tenant", "acme")
	hc.Set("x-md-global-ignored", "no")
	ctx := transport.NewServerContext(t.Context(), &testTransport{hc})

	next := ServerStream(WithPropagatedPrefix("x-forge-"))(func(_ any, stream middleware.ServerStream) error {
		md, _ := metadata.FromServerContext(stream.Context())
		if v := md.Get("x-forge-tenant"); v != "acme" {
			t.Errorf("x-forge-tenant = %q, want %q", v, "acme")
		}
		if v := md.Get("x-md-global-ignored"); v != "" {
			t.Errorf("x-md-global-ignored = %q, want it not to be propagated", v)
		}
		return nil
	})
	if err := next(nil, &testStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
}

func TestServerStreamWithoutTransportPassesThrough(t *testing.T) {
	called := false
	next := ServerStream()(func(_ any, stream middleware.ServerStream) error {
		called = true
		if _, ok := metadata.FromServerContext(stream.Context()); ok {
			t.Error("metadata should be absent without a server transport")
		}
		return nil
	})
	if err := next(nil, &testStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestServerStreamPreservesStreamCapabilities(t *testing.T) {
	ctx := transport.NewServerContext(t.Context(), &testTransport{headerCarrier{}})
	underlying := &testStream{ctx: ctx}

	next := ServerStream()(func(_ any, stream middleware.ServerStream) error {
		if err := stream.SendMsg("out"); err != nil {
			return err
		}
		return stream.RecvMsg(new(string))
	})
	if err := next(nil, underlying); err != nil {
		t.Fatal(err)
	}
	if underlying.sent != 1 {
		t.Errorf("SendMsg calls = %d, want 1", underlying.sent)
	}
	if underlying.received != 1 {
		t.Errorf("RecvMsg calls = %d, want 1", underlying.received)
	}
}

type testStream struct {
	ctx      context.Context
	sent     int
	received int
}

func (s *testStream) Context() context.Context { return s.ctx }
func (s *testStream) SendMsg(any) error        { s.sent++; return nil }
func (s *testStream) RecvMsg(any) error        { s.received++; return nil }
