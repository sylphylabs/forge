package ratelimit

import (
	"context"
	"errors"
	"testing"

	internalratelimit "github.com/sylphylabs/forge/internal/ratelimit"
	"github.com/sylphylabs/forge/middleware"
)

type (
	ratelimitMock struct {
		reached bool
	}
	ratelimitReachedMock struct {
		reached bool
	}
)

func (r *ratelimitMock) Allow() (DoneFunc, error) {
	return func(_ DoneInfo) {
		r.reached = true
	}, nil
}

func (r *ratelimitReachedMock) Allow() (DoneFunc, error) {
	return func(_ DoneInfo) {
		r.reached = true
	}, errors.New("errored")
}

func Test_WithLimiter(t *testing.T) {
	o := options{
		limiter: &ratelimitMock{},
	}

	WithLimiter(nil)(&o)
	if o.limiter != nil {
		t.Error("The limiter property must be updated.")
	}
}

func TestServer(t *testing.T) {
	nextValid := func(context.Context, any) (any, error) {
		return "Hello valid", nil
	}

	rlm := &ratelimitMock{}
	rlrm := &ratelimitReachedMock{}

	_, _ = Server(func(o *options) {
		o.limiter = rlm
	})(nextValid)(context.Background(), nil)
	if !rlm.reached {
		t.Error("The ratelimit must run the done function.")
	}

	_, _ = Server(func(o *options) {
		o.limiter = rlrm
	})(nextValid)(context.Background(), nil)
	if rlrm.reached {
		t.Error("The ratelimit must not run the done function and should be denied.")
	}
}

func TestServerPanicReleasesInFlight(t *testing.T) {
	limiter := internalratelimit.NewLimiter()
	next := func(context.Context, any) (any, error) {
		panic("handler panic")
	}
	handler := Server(WithLimiter(limiter))(next)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must propagate through the middleware")
			}
		}()
		_, _ = handler(context.Background(), nil)
	}()

	if got := limiter.Stat().InFlight; got != 0 {
		t.Errorf("InFlight = %d after panic, want 0", got)
	}
}

func TestServerStreamPanicReleasesInFlight(t *testing.T) {
	limiter := internalratelimit.NewLimiter()
	handler := ServerStream(WithLimiter(limiter))(func(any, middleware.ServerStream) error {
		panic("handler panic")
	})

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must propagate through the middleware")
			}
		}()
		_ = handler(nil, &testStream{ctx: context.Background()})
	}()

	if got := limiter.Stat().InFlight; got != 0 {
		t.Errorf("InFlight = %d after panic, want 0", got)
	}
}

func TestPerMessageServerStreamPanicReleasesInFlight(t *testing.T) {
	limiter := internalratelimit.NewLimiter()
	handler := PerMessageServerStream(WithLimiter(limiter))(func(_ any, stream middleware.ServerStream) error {
		return stream.RecvMsg(new(string))
	})

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic must propagate through the middleware")
			}
		}()
		_ = handler(nil, &panickingStream{testStream: testStream{ctx: context.Background()}})
	}()

	if got := limiter.Stat().InFlight; got != 0 {
		t.Errorf("InFlight = %d after panic, want 0", got)
	}
}

// panickingStream panics on RecvMsg, standing in for a transport failure that
// unwinds through the per-message limiter.
type panickingStream struct {
	testStream
}

func (*panickingStream) RecvMsg(any) error {
	panic("recv panic")
}
