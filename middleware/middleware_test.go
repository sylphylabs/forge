package middleware

import (
	"context"
	"errors"
	"testing"
)

func TestChainUnary(t *testing.T) {
	var events []string
	mw := func(name string) UnaryMiddleware {
		return func(next UnaryHandler) UnaryHandler {
			return func(ctx context.Context, request any) (any, error) {
				events = append(events, name+":before")
				reply, err := next(ctx, request)
				events = append(events, name+":after")
				return reply, err
			}
		}
	}
	handler := ChainUnary(mw("first"), mw("second"))(func(_ context.Context, request any) (any, error) {
		events = append(events, "handler:"+request.(string))
		return "reply", nil
	})

	reply, err := handler(t.Context(), "request")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "reply" {
		t.Fatalf("reply = %v, want reply", reply)
	}
	want := []string{"first:before", "second:before", "handler:request", "second:after", "first:after"}
	if !equalStrings(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestChainUnaryPreservesError(t *testing.T) {
	want := errors.New("terminal")
	handler := ChainUnary(func(next UnaryHandler) UnaryHandler { return next })(func(context.Context, any) (any, error) {
		return nil, want
	})
	_, got := handler(t.Context(), nil)
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestChainStream(t *testing.T) {
	var events []string
	mw := func(name string) StreamMiddleware {
		return func(next StreamHandler) StreamHandler {
			return func(request any, stream ServerStream) error {
				events = append(events, name+":before")
				err := next(request, stream)
				events = append(events, name+":after")
				return err
			}
		}
	}
	handler := ChainStream(mw("first"), mw("second"))(func(request any, _ ServerStream) error {
		events = append(events, "handler:"+request.(string))
		return nil
	})

	if err := handler("request", &testStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
	want := []string{"first:before", "second:before", "handler:request", "second:after", "first:after"}
	if !equalStrings(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

type contextKey struct{}

func TestStreamMiddlewareCanDecorateContext(t *testing.T) {
	wrapped := ChainStream(func(next StreamHandler) StreamHandler {
		return func(request any, stream ServerStream) error {
			return next(request, contextStream{
				ServerStream: stream,
				ctx:          context.WithValue(stream.Context(), contextKey{}, "decorated"),
			})
		}
	})(func(_ any, stream ServerStream) error {
		if got := stream.Context().Value(contextKey{}); got != "decorated" {
			t.Fatalf("context value = %v, want decorated", got)
		}
		return nil
	})

	if err := wrapped(nil, &testStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}
}

type testStream struct {
	ctx context.Context
}

func (s *testStream) Context() context.Context { return s.ctx }
func (*testStream) SendMsg(any) error          { return nil }
func (*testStream) RecvMsg(any) error          { return nil }

type contextStream struct {
	ServerStream
	ctx context.Context
}

func (s contextStream) Context() context.Context { return s.ctx }

func equalStrings(got, want []string) bool {
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
