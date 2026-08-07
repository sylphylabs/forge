package validate

import (
	"context"
	"errors"
	"testing"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

type streamMessage struct {
	err error
}

func (m *streamMessage) Validate() error { return m.err }

func TestValidatorStreamValidatesInitialRequest(t *testing.T) {
	want := errors.New("invalid initial request")
	called := false

	err := ValidatorStream()(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})(&streamMessage{err: want}, &recvStream{ctx: t.Context()})

	if err == nil {
		t.Fatal("ValidatorStream() error = nil, want error")
	}
	if !errors.Is(err, want) {
		t.Errorf("ValidatorStream() error = %v, want it to wrap %v", err, want)
	}
	if se := forgeerrors.FromError(err); se == nil || se.Reason != "VALIDATOR" {
		t.Errorf("reason = %v, want VALIDATOR", se)
	}
	if called {
		t.Error("handler must not run when the initial request is invalid")
	}
}

func TestValidatorStreamAcceptsValidInitialRequest(t *testing.T) {
	called := false
	err := ValidatorStream()(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})(&streamMessage{}, &recvStream{ctx: t.Context()})
	if err != nil {
		t.Fatalf("ValidatorStream() error = %v, want nil", err)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestValidatorStreamSkipsNilRequest(t *testing.T) {
	validatorCalls := 0
	err := ValidatorStream(func(any) error {
		validatorCalls++
		return nil
	})(func(any, middleware.ServerStream) error { return nil })(nil, &recvStream{ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if validatorCalls != 0 {
		t.Errorf("validator calls = %d, want 0 for bidirectional streams", validatorCalls)
	}
}

func TestValidatorStreamDoesNotValidateMessages(t *testing.T) {
	stream := &recvStream{ctx: t.Context(), msg: &streamMessage{err: errors.New("bad message")}}

	err := ValidatorStream()(func(_ any, s middleware.ServerStream) error {
		// ValidatorStream only checks the initial request, so a bad message passes.
		return s.RecvMsg(new(streamMessage))
	})(nil, stream)
	if err != nil {
		t.Errorf("ValidatorStream() error = %v, want nil; messages are not validated", err)
	}
}

func TestPerMessageValidatorStreamValidatesEachMessage(t *testing.T) {
	want := errors.New("bad message")
	stream := &recvStream{ctx: t.Context(), msg: &streamMessage{err: want}}
	var recvErr error

	err := PerMessageValidatorStream()(func(_ any, s middleware.ServerStream) error {
		recvErr = s.RecvMsg(new(streamMessage))
		// The handler stays in control after a rejected message.
		return nil
	})(nil, stream)
	if err != nil {
		t.Errorf("PerMessageValidatorStream() error = %v, want nil; the stream stays open", err)
	}
	if recvErr == nil || !errors.Is(recvErr, want) {
		t.Errorf("RecvMsg() error = %v, want it to wrap %v", recvErr, want)
	}
}

func TestPerMessageValidatorStreamAcceptsValidMessages(t *testing.T) {
	stream := &recvStream{ctx: t.Context(), msg: &streamMessage{}}

	err := PerMessageValidatorStream()(func(_ any, s middleware.ServerStream) error {
		return s.RecvMsg(new(streamMessage))
	})(nil, stream)
	if err != nil {
		t.Errorf("PerMessageValidatorStream() error = %v, want nil", err)
	}
}

func TestPerMessageValidatorStreamRunsCustomValidators(t *testing.T) {
	want := errors.New("rejected by validator")
	stream := &recvStream{ctx: t.Context(), msg: &streamMessage{}}
	var recvErr error

	err := PerMessageValidatorStream(func(any) error { return want })(
		func(_ any, s middleware.ServerStream) error {
			recvErr = s.RecvMsg(new(streamMessage))
			return nil
		},
	)(nil, stream)
	if err != nil {
		t.Fatal(err)
	}
	if recvErr == nil || !errors.Is(recvErr, want) {
		t.Errorf("RecvMsg() error = %v, want it to wrap %v", recvErr, want)
	}
}

func TestPerMessageValidatorStreamPropagatesRecvError(t *testing.T) {
	want := errors.New("transport closed")
	stream := &recvStream{ctx: t.Context(), recvErr: want}
	var recvErr error

	_ = PerMessageValidatorStream()(func(_ any, s middleware.ServerStream) error {
		recvErr = s.RecvMsg(new(streamMessage))
		return nil
	})(nil, stream)

	if recvErr != want {
		t.Errorf("RecvMsg() error = %v, want the transport error %v unwrapped", recvErr, want)
	}
}

// recvStream hands msg to the caller of RecvMsg, mimicking a decoded message.
type recvStream struct {
	ctx     context.Context
	msg     *streamMessage
	recvErr error
}

func (s *recvStream) Context() context.Context { return s.ctx }
func (*recvStream) SendMsg(any) error          { return nil }

func (s *recvStream) RecvMsg(m any) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.msg != nil {
		if target, ok := m.(*streamMessage); ok {
			*target = *s.msg
		}
	}
	return nil
}
