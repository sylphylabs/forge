package selector

import (
	"context"
	"testing"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// tracingStream records that it ran, so tests can assert whether the selector
// applied the middleware for a given operation.
func tracingStream(applied *bool) middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			*applied = true
			return next(request, stream)
		}
	}
}

func TestServerStreamSelectorMatches(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		build     func(*bool) (middleware.StreamMiddleware, error)
		want      bool
	}{
		{
			name:      "prefix matches",
			operation: "/example/forge/SayHello",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Prefix("/example").Build()
			},
			want: true,
		},
		{
			name:      "prefix does not match",
			operation: "/other/forge/SayHello",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Prefix("/example").Build()
			},
			want: false,
		},
		{
			name:      "path matches",
			operation: "/example/forge",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Path("/example/forge").Build()
			},
			want: true,
		},
		{
			name:      "regex matches",
			operation: "/example/forge",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Regex("/example/.*").Build()
			},
			want: true,
		},
		{
			name:      "match func matches",
			operation: "/example/forge",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Match(func(_ context.Context, operation string) bool {
					return operation == "/example/forge"
				}).Build()
			},
			want: true,
		},
		{
			name:      "no rule matches nothing",
			operation: "/example/forge",
			build: func(a *bool) (middleware.StreamMiddleware, error) {
				return ServerStream(tracingStream(a)).Build()
			},
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			ctx := transport.NewServerContext(t.Context(), &Transport{operation: test.operation})

			m, err := test.build(&applied)
			if err != nil {
				t.Fatal(err)
			}
			called := false
			handler := m(func(any, middleware.ServerStream) error {
				called = true
				return nil
			})
			if err := handler(nil, &streamStub{ctx: ctx}); err != nil {
				t.Fatal(err)
			}

			if applied != test.want {
				t.Errorf("middleware applied = %v, want %v", applied, test.want)
			}
			if !called {
				t.Error("handler must run whether or not the selector matches")
			}
		})
	}
}

func TestClientStreamSelectorUsesClientContext(t *testing.T) {
	applied := false
	ctx := transport.NewClientContext(t.Context(), &Transport{operation: "/example/forge"})

	m, err := ClientStream(tracingStream(&applied)).Prefix("/example").Build()
	if err != nil {
		t.Fatal(err)
	}
	handler := m(func(any, middleware.ServerStream) error { return nil })
	if err := handler(nil, &streamStub{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Error("client stream selector did not apply middleware")
	}
}

func TestStreamSelectorWithoutTransport(t *testing.T) {
	applied := false
	m, err := ServerStream(tracingStream(&applied)).Prefix("/example").Build()
	if err != nil {
		t.Fatal(err)
	}
	handler := m(func(any, middleware.ServerStream) error { return nil })
	if err := handler(nil, &streamStub{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("middleware must not apply without a transport in context")
	}
}

func TestStreamSelectorInvalidRegexFailsBuild(t *testing.T) {
	m, err := ServerStream().Regex("^\b(?").Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for invalid regex")
	}
	if m != nil {
		t.Errorf("Build() middleware = %v, want nil on error", m)
	}
}

// TestStreamSelectorComposesOnce asserts the Request-Path Contract: the
// selected chain is composed when the middleware wraps its handler, not per
// stream.
func TestStreamSelectorComposesOnce(t *testing.T) {
	var compositions, calls int
	m := func(next middleware.StreamHandler) middleware.StreamHandler {
		compositions++
		return func(request any, stream middleware.ServerStream) error {
			calls++
			return next(request, stream)
		}
	}

	built, err := ServerStream(m).Prefix("/example").Build()
	if err != nil {
		t.Fatal(err)
	}
	handler := built(func(any, middleware.ServerStream) error { return nil })

	ctx := transport.NewServerContext(t.Context(), &Transport{operation: "/example/forge"})
	for range 3 {
		if err := handler(nil, &streamStub{ctx: ctx}); err != nil {
			t.Fatal(err)
		}
	}

	if compositions != 1 {
		t.Errorf("middleware compositions = %d, want 1", compositions)
	}
	if calls != 3 {
		t.Errorf("middleware calls = %d, want 3", calls)
	}
}

type streamStub struct {
	ctx context.Context
}

func (s *streamStub) Context() context.Context { return s.ctx }
func (*streamStub) SendMsg(any) error          { return nil }
func (*streamStub) RecvMsg(any) error          { return nil }
