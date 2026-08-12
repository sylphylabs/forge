package env

import (
	"context"
	"sync"

	"github.com/sylphylabs/forge/config"
)

var _ config.Watcher = (*watcher)(nil)

// watcher reports no changes: the process environment is fixed at startup,
// so Next blocks until the watcher is stopped or ctx is done.
type watcher struct {
	exit chan struct{}
	once sync.Once
}

func newWatcher() *watcher {
	return &watcher{exit: make(chan struct{})}
}

func (w *watcher) Next(ctx context.Context) ([]*config.KeyValue, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-w.exit:
		return nil, context.Canceled
	}
}

// Stop unblocks an in-flight Next. It is safe to call more than once.
func (w *watcher) Stop() error {
	w.once.Do(func() { close(w.exit) })
	return nil
}
