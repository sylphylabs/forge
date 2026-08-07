package recovery

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

func TestRecoveryStreamRecoversPanic(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Error("panic escaped Stream")
		}
	}()

	next := func(any, middleware.ServerStream) error {
		panic("panic reason")
	}
	err := Stream(WithHandler(func(ctx context.Context, _, rerr any) error {
		if _, ok := ctx.Value(Latency{}).(float64); !ok {
			t.Error("latency missing from context")
		}
		return errors.InternalServer("RECOVERY", fmt.Sprintf("panic triggered: %v", rerr))
	}))(next)(nil, &testStream{ctx: t.Context()})

	if err == nil {
		t.Fatal("Stream() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "panic reason") {
		t.Errorf("Stream() error = %v, want containing %q", err, "panic reason")
	}
}

func TestRecoveryStreamPassesRequestToHandler(t *testing.T) {
	next := func(any, middleware.ServerStream) error {
		panic("boom")
	}
	var got any
	_ = Stream(WithHandler(func(_ context.Context, req, _ any) error {
		got = req
		return ErrUnknownRequest
	}))(next)("initial", &testStream{ctx: t.Context()})

	if got != "initial" {
		t.Errorf("handler request = %v, want %v", got, "initial")
	}
}

func TestRecoveryStreamWithoutPanic(t *testing.T) {
	next := func(_ any, stream middleware.ServerStream) error {
		return stream.SendMsg("reply")
	}
	if err := Stream()(next)(nil, &testStream{ctx: t.Context()}); err != nil {
		t.Errorf("Stream() error = %v, want nil", err)
	}
}

func TestRecoveryStreamPropagatesHandlerError(t *testing.T) {
	want := errors.InternalServer("STREAM", "stream failed")
	next := func(any, middleware.ServerStream) error {
		return want
	}
	if err := Stream()(next)(nil, &testStream{ctx: t.Context()}); err != want {
		t.Errorf("Stream() error = %v, want %v", err, want)
	}
}

func TestRecoveryStreamDefaultHandler(t *testing.T) {
	next := func(any, middleware.ServerStream) error {
		panic("boom")
	}
	err := Stream()(next)(nil, &testStream{ctx: t.Context()})
	if !errors.Is(err, ErrUnknownRequest) {
		t.Errorf("Stream() error = %v, want %v", err, ErrUnknownRequest)
	}
}

type testStream struct {
	ctx context.Context
}

func (s *testStream) Context() context.Context { return s.ctx }
func (*testStream) SendMsg(any) error          { return nil }
func (*testStream) RecvMsg(any) error          { return nil }
