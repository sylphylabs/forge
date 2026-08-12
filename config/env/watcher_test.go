package env

import (
	"context"
	"testing"
)

func Test_watcher_next(t *testing.T) {
	t.Run("next after stop should return err", func(t *testing.T) {
		w := newWatcher()
		_ = w.Stop()
		if _, err := w.Next(context.Background()); err == nil {
			t.Error("expect error, actual nil")
		}
	})

	t.Run("next after ctx cancel should return err", func(t *testing.T) {
		w := newWatcher()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := w.Next(ctx); err == nil {
			t.Error("expect error, actual nil")
		}
	})
}

func Test_watcher_stop(t *testing.T) {
	t.Run("stop multiple times should not panic", func(t *testing.T) {
		w := newWatcher()
		_ = w.Stop()
		_ = w.Stop()
	})
}
