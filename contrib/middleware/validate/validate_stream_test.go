package validate

import (
	"context"
	"errors"
	"testing"

	"github.com/sylphylabs/forge/contrib/middleware/validate/internal/testdata"
	kerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

func TestProtoValidateStreamInitialRequest(t *testing.T) {
	tests := []testcase{
		{name: "valid_modern", req: &testdata.Modern{Name: "testcase", Age: 19}, err: false},
		{name: "invalid_modern", req: &testdata.Modern{Name: "test", Age: 19}, err: true},
		{name: "valid_legacy", req: &legacyRequest{}, err: false},
		{name: "invalid_legacy", req: &legacyRequest{err: errors.New("legacy validation failed")}, err: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			err := ProtoValidateStream()(func(any, middleware.ServerStream) error {
				called = true
				return nil
			})(test.req, &recvStream{ctx: t.Context()})

			if test.err {
				if err == nil {
					t.Fatal("ProtoValidateStream() error = nil, want error")
				}
				if se := kerrors.FromError(err); se == nil || se.Reason != "VALIDATOR" {
					t.Errorf("reason = %v, want VALIDATOR", se)
				}
				if called {
					t.Error("handler must not run when the initial request is invalid")
				}
				return
			}
			if err != nil {
				t.Fatalf("ProtoValidateStream() error = %v, want nil", err)
			}
			if !called {
				t.Error("handler was not called")
			}
		})
	}
}

func TestProtoValidateStreamSkipsNilRequest(t *testing.T) {
	called := false
	err := ProtoValidateStream()(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})(nil, &recvStream{ctx: t.Context()})
	if err != nil {
		t.Fatalf("ProtoValidateStream() error = %v, want nil", err)
	}
	if !called {
		t.Error("handler was not called for a bidirectional stream")
	}
}

func TestProtoValidateStreamDoesNotValidateMessages(t *testing.T) {
	stream := &recvStream{ctx: t.Context(), msg: &testdata.Modern{Name: "test", Age: 19}}

	err := ProtoValidateStream()(func(_ any, s middleware.ServerStream) error {
		return s.RecvMsg(&testdata.Modern{})
	})(nil, stream)
	if err != nil {
		t.Errorf("ProtoValidateStream() error = %v, want nil; messages are not validated", err)
	}
}

func TestPerMessageProtoValidateStreamRejectsInvalidMessage(t *testing.T) {
	stream := &recvStream{ctx: t.Context(), msg: &testdata.Modern{Name: "test", Age: 19}}
	var recvErr error

	err := PerMessageProtoValidateStream()(func(_ any, s middleware.ServerStream) error {
		recvErr = s.RecvMsg(&testdata.Modern{})
		// The handler stays in control after a rejected message.
		return nil
	})(nil, stream)
	if err != nil {
		t.Errorf("PerMessageProtoValidateStream() error = %v, want nil; the stream stays open", err)
	}
	if recvErr == nil {
		t.Fatal("RecvMsg() error = nil, want a validation error")
	}
	if se := kerrors.FromError(recvErr); se == nil || se.Reason != "VALIDATOR" {
		t.Errorf("reason = %v, want VALIDATOR", se)
	}
}

func TestPerMessageProtoValidateStreamAcceptsValidMessage(t *testing.T) {
	stream := &recvStream{ctx: t.Context(), msg: &testdata.Modern{Name: "testcase", Age: 19}}

	err := PerMessageProtoValidateStream()(func(_ any, s middleware.ServerStream) error {
		return s.RecvMsg(&testdata.Modern{})
	})(nil, stream)
	if err != nil {
		t.Errorf("PerMessageProtoValidateStream() error = %v, want nil", err)
	}
}

func TestPerMessageProtoValidateStreamPropagatesRecvError(t *testing.T) {
	want := errors.New("transport closed")
	stream := &recvStream{ctx: t.Context(), recvErr: want}
	var recvErr error

	_ = PerMessageProtoValidateStream()(func(_ any, s middleware.ServerStream) error {
		recvErr = s.RecvMsg(&testdata.Modern{})
		return nil
	})(nil, stream)

	if recvErr != want {
		t.Errorf("RecvMsg() error = %v, want the transport error %v unwrapped", recvErr, want)
	}
}

// recvStream hands msg to the caller of RecvMsg, mimicking a decoded message.
type recvStream struct {
	ctx     context.Context
	msg     *testdata.Modern
	recvErr error
}

func (s *recvStream) Context() context.Context { return s.ctx }
func (*recvStream) SendMsg(any) error          { return nil }

func (s *recvStream) RecvMsg(m any) error {
	if s.recvErr != nil {
		return s.recvErr
	}
	if s.msg != nil {
		if target, ok := m.(*testdata.Modern); ok {
			target.Name = s.msg.GetName()
			target.Age = s.msg.GetAge()
		}
	}
	return nil
}
