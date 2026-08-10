package message

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/sylphylabs/forge/middleware"
)

func TestMessageOwnsPortableFields(t *testing.T) {
	body := []byte("payload")
	msg := New(body)
	body[0] = 'X'
	if got := string(msg.Body); got != "payload" {
		t.Fatalf("Body = %q, want payload", got)
	}

	msg.ID = "event-1"
	msg.Key = "account-1"
	msg.SetHeader("TraceParent", "first")
	msg.AddHeader("traceparent", "second")
	if got := msg.Header("TRACEPARENT"); got != "first" {
		t.Fatalf("Header(traceparent) = %q, want first", got)
	}

	clone := msg.Clone()
	clone.Body[0] = 'X'
	clone.Headers.Set("traceparent", "changed")
	if got := string(msg.Body); got != "payload" {
		t.Errorf("original Body changed to %q", got)
	}
	if got := msg.Header("traceparent"); got != "first" {
		t.Errorf("original header changed to %q", got)
	}
	if got := clone.ID; got != "event-1" {
		t.Errorf("clone ID = %q, want event-1", got)
	}
	if got := clone.Key; got != "account-1" {
		t.Errorf("clone Key = %q, want account-1", got)
	}
}

func TestNilMessageHelpers(t *testing.T) {
	var msg *Message
	msg.SetHeader("key", "value")
	msg.AddHeader("key", "value")
	if got := msg.Header("key"); got != "" {
		t.Errorf("Header = %q, want empty", got)
	}
	if clone := msg.Clone(); clone != nil {
		t.Errorf("Clone = %#v, want nil", clone)
	}
}

// Composition is the middleware package's, so a consumer gets the same ordering
// guarantee an HTTP or gRPC handler gets — and the same treatment of a nil
// entry, which that package rejects rather than skipping.
func TestChainedMiddlewareRunsInDeclarationOrder(t *testing.T) {
	var calls []string
	record := func(name string) middleware.UnaryMiddleware {
		return func(next middleware.UnaryHandler) middleware.UnaryHandler {
			return func(ctx context.Context, req any) (any, error) {
				calls = append(calls, name+":before")
				reply, err := next(ctx, req)
				calls = append(calls, name+":after")
				return reply, err
			}
		}
	}
	wantErr := errors.New("handler failed")
	handler := middleware.ChainUnary(record("outer"), record("inner"))(
		func(context.Context, any) (any, error) {
			calls = append(calls, "handler")
			return nil, wantErr
		})

	if _, err := handler(context.Background(), New(nil)); !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v, want %v", err, wantErr)
	}
	want := []string{"outer:before", "inner:before", "handler", "inner:after", "outer:after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
