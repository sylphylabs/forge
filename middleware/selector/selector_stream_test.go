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
		build     func(*bool) middleware.StreamMiddleware
		want      bool
	}{
		{
			name:      "prefix matches",
			operation: "/example/forge/SayHello",
			build: func(a *bool) middleware.StreamMiddleware {
				return ServerStream(tracingStream(a)).Prefix("/example").Build()
			},
			want: true,
		},
		{
			name:      "prefix does not match",
			operation: "/other/forge/SayHello",
			build: func(a *bool) middleware.StreamMiddleware {
				return ServerStream(tracingStream(a)).Prefix("/example").Build()
			},
			want: false,
		},
		{
			name:      "path matches",
			operation: "/example/forge",
			build: func(a *bool) middleware.StreamMiddleware {
				return ServerStream(tracingStream(a)).Path("/example/forge").Build()
			},
			want: true,
		},
		{
			name:      "regex matches",
			operation: "/example/forge",
			build: func(a *bool) middleware.StreamMiddleware {
				return ServerStream(tracingStream(a)).Regex("/example/.*").Build()
			},
			want: true,
		},
		{
			name:      "match func matches",
			operation: "/example/forge",
			build: func(a *bool) middleware.StreamMiddleware {
				return ServerStream(tracingStream(a)).Match(func(_ context.Context, operation string) bool {
					return operation == "/example/forge"
				}).Build()
			},
			want: true,
		},
		{
			name:      "no rule matches nothing",
			operation: "/example/forge",
			build:     func(a *bool) middleware.StreamMiddleware { return ServerStream(tracingStream(a)).Build() },
			want:      false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			ctx := transport.NewServerContext(t.Context(), &Transport{operation: test.operation})

			called := false
			handler := test.build(&applied)(func(any, middleware.ServerStream) error {
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

	handler := ClientStream(tracingStream(&applied)).Prefix("/example").Build()(
		func(any, middleware.ServerStream) error { return nil },
	)
	if err := handler(nil, &streamStub{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	if !applied {
		t.Error("client stream selector did not apply middleware")
	}
}

func TestStreamSelectorWithoutTransport(t *testing.T) {
	applied := false
	handler := ServerStream(tracingStream(&applied)).Prefix("/example").Build()(
		func(any, middleware.ServerStream) error { return nil },
	)
	if err := handler(nil, &streamStub{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Error("middleware must not apply without a transport in context")
	}
}

type streamStub struct {
	ctx context.Context
}

func (s *streamStub) Context() context.Context { return s.ctx }
func (*streamStub) SendMsg(any) error          { return nil }
func (*streamStub) RecvMsg(any) error          { return nil }
