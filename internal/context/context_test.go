package context

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type contextKey string

func TestMergeValues(t *testing.T) {
	parent1 := context.WithValue(context.Background(), contextKey("first"), "parent1")
	parent1 = context.WithValue(parent1, contextKey("shared"), "parent1")
	parent2 := context.WithValue(context.Background(), contextKey("second"), "parent2")
	parent2 = context.WithValue(parent2, contextKey("shared"), "parent2")

	ctx, cancel := Merge(parent1, parent2)
	defer cancel()

	for key, want := range map[contextKey]string{
		"first":  "parent1",
		"second": "parent2",
		"shared": "parent1",
	} {
		if got := ctx.Value(key); got != want {
			t.Errorf("Value(%q) = %v, want %q", key, got, want)
		}
	}
}

func TestMergeDeadline(t *testing.T) {
	now := time.Now()
	earlier := now.Add(time.Hour)
	later := now.Add(2 * time.Hour)
	parent1, cancel1 := context.WithDeadline(context.Background(), later)
	defer cancel1()
	parent2, cancel2 := context.WithDeadline(context.Background(), earlier)
	defer cancel2()

	ctx, cancel := Merge(parent1, parent2)
	defer cancel()
	got, ok := ctx.Deadline()
	if !ok {
		t.Fatal("Deadline() reported no deadline")
	}
	if !got.Equal(earlier) {
		t.Fatalf("Deadline() = %v, want %v", got, earlier)
	}

	ctx, cancel = Merge(context.Background(), parent1)
	defer cancel()
	got, ok = ctx.Deadline()
	if !ok || !got.Equal(later) {
		t.Fatalf("Deadline() = (%v, %v), want (%v, true)", got, ok, later)
	}

	ctx, cancel = Merge(context.Background(), context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("Deadline() reported a deadline")
	}
}

func TestMergeCancellation(t *testing.T) {
	for _, test := range []struct {
		name   string
		cancel func(context.CancelFunc, context.CancelFunc, context.CancelFunc)
	}{
		{
			name: "parent1",
			cancel: func(cancel1, _, _ context.CancelFunc) {
				cancel1()
			},
		},
		{
			name: "parent2",
			cancel: func(_, cancel2, _ context.CancelFunc) {
				cancel2()
			},
		},
		{
			name: "explicit",
			cancel: func(_, _, cancel context.CancelFunc) {
				cancel()
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent1, cancel1 := context.WithCancel(context.Background())
			defer cancel1()
			parent2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			ctx, cancel := Merge(parent1, parent2)
			defer cancel()

			test.cancel(cancel1, cancel2, cancel)
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
			}
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("Done() was not closed")
			}
		})
	}
}

func TestMergeAlreadyCanceled(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	ctx, cancel := Merge(context.Background(), parent)
	defer cancel()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("Done() is not closed")
	}
}

func TestMergeDeadlineExceeded(t *testing.T) {
	for _, test := range []struct {
		name    string
		parents func(context.Context) (context.Context, context.Context)
	}{
		{
			name: "parent1",
			parents: func(expired context.Context) (context.Context, context.Context) {
				return expired, context.Background()
			},
		},
		{
			name: "parent2",
			parents: func(expired context.Context) (context.Context, context.Context) {
				return context.Background(), expired
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancelExpired()
			parent1, parent2 := test.parents(expired)
			ctx, cancel := Merge(parent1, parent2)
			defer cancel()
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("Err() = %v, want %v", ctx.Err(), context.DeadlineExceeded)
			}
		})
	}
}

func TestMergePreservesParentCauseLookup(t *testing.T) {
	want := errors.New("parent2 cause")
	parent2, cancelParent2 := context.WithCancelCause(context.Background())
	ctx, cancel := Merge(context.Background(), parent2)
	defer cancel()

	cancelParent2(want)
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
	}
	if !errors.Is(context.Cause(ctx), want) {
		t.Fatalf("Cause() = %v, want %v", context.Cause(ctx), want)
	}
}

func TestMergeConcurrentCancellation(t *testing.T) {
	for range 100 {
		parent1, cancel1 := context.WithCancel(context.Background())
		parent2, cancel2 := context.WithCancel(context.Background())
		ctx, cancel := Merge(parent1, parent2)

		var wg sync.WaitGroup
		wg.Go(cancel1)
		wg.Go(cancel2)
		wg.Go(cancel)
		wg.Wait()
		<-ctx.Done()
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("Err() = %v, want %v", ctx.Err(), context.Canceled)
		}
	}
}

func TestMergeRemovesCancellationCallbacks(t *testing.T) {
	parent1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	parent2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	ctx, cancel := Merge(parent1, parent2)
	cancel()
	<-ctx.Done()

	mc := ctx.(*mergeCtx)
	if mc.stopParent2 != nil && mc.stopParent2() {
		t.Fatal("parent2 cancellation callback remained registered")
	}
}

func BenchmarkMerge(b *testing.B) {
	parent1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	parent2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	b.ReportAllocs()
	for b.Loop() {
		ctx, cancel := Merge(parent1, parent2)
		cancel()
		<-ctx.Done()
	}
}
