package context

import (
	"context"
	"time"
)

type mergeCtx struct {
	parent1, parent2 context.Context
	cancelCtx        context.Context
	cancelCause      context.CancelCauseFunc
	stopParent2      func() bool
}

type parent2Cause struct {
	err error
}

func (c parent2Cause) Error() string {
	return c.err.Error()
}

func (c parent2Cause) Unwrap() error {
	return c.err
}

// Merge merges two contexts into one. The returned cancel function must be
// called to release the cancellation callback registered with parent2.
func Merge(parent1, parent2 context.Context) (context.Context, context.CancelFunc) {
	cancelCtx, cancelCause := context.WithCancelCause(parent1)
	mc := &mergeCtx{
		parent1:     parent1,
		parent2:     parent2,
		cancelCtx:   cancelCtx,
		cancelCause: cancelCause,
	}
	select {
	case <-parent2.Done():
		mc.cancelFromParent2()
	default:
		if parent2.Done() != nil {
			mc.stopParent2 = context.AfterFunc(parent2, mc.cancelFromParent2)
		}
	}
	return mc, mc.cancel
}

func (mc *mergeCtx) cancelFromParent2() {
	mc.cancelCause(parent2Cause{err: mc.parent2.Err()})
}

func (mc *mergeCtx) cancel() {
	if mc.stopParent2 != nil {
		mc.stopParent2()
	}
	mc.cancelCause(context.Canceled)
}

// Done implements context.Context.
func (mc *mergeCtx) Done() <-chan struct{} {
	return mc.cancelCtx.Done()
}

// Err implements context.Context.
func (mc *mergeCtx) Err() error {
	if mc.cancelCtx.Err() == nil {
		select {
		case <-mc.parent2.Done():
			mc.cancelFromParent2()
		default:
			return nil
		}
	}
	if cause, ok := context.Cause(mc.cancelCtx).(parent2Cause); ok {
		return cause.err
	}
	return mc.cancelCtx.Err()
}

// Deadline implements context.Context.
func (mc *mergeCtx) Deadline() (time.Time, bool) {
	d1, ok1 := mc.parent1.Deadline()
	d2, ok2 := mc.parent2.Deadline()
	switch {
	case !ok1:
		return d2, ok2
	case !ok2:
		return d1, ok1
	case d1.Before(d2):
		return d1, true
	default:
		return d2, true
	}
}

// Value implements context.Context.
func (mc *mergeCtx) Value(key any) any {
	if v := mc.parent1.Value(key); v != nil {
		return v
	}
	return mc.parent2.Value(key)
}
