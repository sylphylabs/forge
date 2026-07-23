package message

import (
	"context"
	"errors"
	"reflect"
	"testing"
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

func TestChain(t *testing.T) {
	var calls []string
	middleware := func(name string) Middleware {
		return func(next Handler) Handler {
			return func(ctx context.Context, destination string, msg *Message) error {
				calls = append(calls, name+":before")
				err := next(ctx, destination, msg)
				calls = append(calls, name+":after")
				return err
			}
		}
	}
	wantErr := errors.New("handler failed")
	handler := Chain(middleware("outer"), nil, middleware("inner"))(func(context.Context, string, *Message) error {
		calls = append(calls, "handler")
		return wantErr
	})

	if err := handler(context.Background(), "events.created", New(nil)); !errors.Is(err, wantErr) {
		t.Fatalf("handler error = %v, want %v", err, wantErr)
	}
	want := []string{"outer:before", "inner:before", "handler", "inner:after", "outer:after"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}
