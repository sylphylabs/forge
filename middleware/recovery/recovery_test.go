package recovery

import (
	"context"
	"fmt"
	"testing"

	"github.com/sylphylabs/forge/errors"
)

func TestOnce(t *testing.T) {
	defer func() {
		if recover() != nil {
			t.Error("fail")
		}
	}()

	next := func(context.Context, any) (any, error) {
		panic("panic reason")
	}
	_, e := Recovery(WithHandler(func(_ context.Context, _, err any) error {
		return errors.Of(errors.KindInternal).WithReason("RECOVERY").Msg(fmt.Sprintf("panic triggered: %v", err))
	}))(next)(context.Background(), "panic")
	t.Logf("succ and reason is %v", e)
}

func TestNotPanic(t *testing.T) {
	next := func(_ context.Context, req any) (any, error) {
		return req.(string) + "https://go-kratos.dev", nil
	}

	_, e := Recovery(WithHandler(func(_ context.Context, _ any, err any) error {
		return errors.Of(errors.KindInternal).WithReason("RECOVERY").Msg(fmt.Sprintf("panic triggered: %v", err))
	}))(next)(context.Background(), "notPanic")
	if e != nil {
		t.Errorf("e isn't nil")
	}
}
