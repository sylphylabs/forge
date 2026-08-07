package ratelimit

import (
	"context"
	"testing"

	"github.com/sylphylabs/forge/middleware"
)

func TestServerStreamAllows(t *testing.T) {
	limiter := &ratelimitMock{}
	called := false

	err := ServerStream(WithLimiter(limiter))(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})(nil, &testStream{ctx: t.Context()})
	if err != nil {
		t.Fatalf("ServerStream() error = %v, want nil", err)
	}
	if !called {
		t.Error("handler was not called")
	}
	if !limiter.reached {
		t.Error("done was not called, so the limiter never saw the stream finish")
	}
}

func TestServerStreamRejects(t *testing.T) {
	limiter := &ratelimitReachedMock{}
	called := false

	err := ServerStream(WithLimiter(limiter))(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})(nil, &testStream{ctx: t.Context()})

	if err != ErrLimitExceed {
		t.Errorf("ServerStream() error = %v, want %v", err, ErrLimitExceed)
	}
	if called {
		t.Error("handler must not run when the stream is rejected")
	}
}

func TestServerStreamTakesOneTokenPerStream(t *testing.T) {
	limiter := &countingLimiter{}

	err := ServerStream(WithLimiter(limiter))(func(_ any, stream middleware.ServerStream) error {
		for range 5 {
			if err := stream.RecvMsg(new(string)); err != nil {
				return err
			}
		}
		return nil
	})(nil, &testStream{ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if limiter.allowed != 1 {
		t.Errorf("Allow calls = %d, want 1 for the whole stream", limiter.allowed)
	}
}

func TestPerMessageServerStreamTakesOneTokenPerMessage(t *testing.T) {
	limiter := &countingLimiter{}
	underlying := &testStream{ctx: t.Context()}

	err := PerMessageServerStream(WithLimiter(limiter))(func(_ any, stream middleware.ServerStream) error {
		for range 5 {
			if err := stream.RecvMsg(new(string)); err != nil {
				return err
			}
		}
		// Sends must not consume tokens.
		return stream.SendMsg("out")
	})(nil, underlying)
	if err != nil {
		t.Fatal(err)
	}
	if limiter.allowed != 5 {
		t.Errorf("Allow calls = %d, want 5, one per received message", limiter.allowed)
	}
	if underlying.received != 5 {
		t.Errorf("RecvMsg calls = %d, want 5", underlying.received)
	}
	if underlying.sent != 1 {
		t.Errorf("SendMsg calls = %d, want 1", underlying.sent)
	}
}

func TestPerMessageServerStreamRejectsMessageAndKeepsStreamOpen(t *testing.T) {
	limiter := &ratelimitReachedMock{}
	var recvErr error

	err := PerMessageServerStream(WithLimiter(limiter))(func(_ any, stream middleware.ServerStream) error {
		recvErr = stream.RecvMsg(new(string))
		// The handler stays in control after a rejected message.
		return nil
	})(nil, &testStream{ctx: t.Context()})
	if err != nil {
		t.Errorf("PerMessageServerStream() error = %v, want nil; the stream stays open", err)
	}
	if recvErr != ErrLimitExceed {
		t.Errorf("RecvMsg() error = %v, want %v", recvErr, ErrLimitExceed)
	}
}

func TestPerMessageServerStreamDoesNotLimitStreamStart(t *testing.T) {
	limiter := &countingLimiter{}

	err := PerMessageServerStream(WithLimiter(limiter))(
		func(any, middleware.ServerStream) error { return nil },
	)(nil, &testStream{ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if limiter.allowed != 0 {
		t.Errorf("Allow calls = %d, want 0 when no message is received", limiter.allowed)
	}
}

type countingLimiter struct {
	allowed int
	done    int
}

func (l *countingLimiter) Allow() (DoneFunc, error) {
	l.allowed++
	return func(DoneInfo) { l.done++ }, nil
}

type testStream struct {
	ctx      context.Context
	sent     int
	received int
}

func (s *testStream) Context() context.Context { return s.ctx }
func (s *testStream) SendMsg(any) error        { s.sent++; return nil }
func (s *testStream) RecvMsg(any) error        { s.received++; return nil }
