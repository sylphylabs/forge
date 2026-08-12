package file

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/sylphylabs/forge/config"
)

var _ config.Watcher = (*watcher)(nil)

type watcher struct {
	f  *file
	fw *fsnotify.Watcher

	exit chan struct{}
	once sync.Once
}

func newWatcher(f *file) (config.Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(f.path); err != nil {
		_ = fw.Close()
		return nil, err
	}
	return &watcher{f: f, fw: fw, exit: make(chan struct{})}, nil
}

func (w *watcher) Next(ctx context.Context) ([]*config.KeyValue, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-w.exit:
			return nil, context.Canceled
		case event, ok := <-w.fw.Events:
			if !ok {
				return nil, context.Canceled
			}
			if skipFile(filepath.Base(event.Name)) {
				continue
			}
			if event.Has(fsnotify.Rename) {
				if _, err := os.Stat(event.Name); err == nil || os.IsExist(err) {
					if err := w.fw.Add(event.Name); err != nil {
						return nil, err
					}
				}
			}
			fi, err := os.Stat(w.f.path)
			if err != nil {
				return nil, err
			}
			path := w.f.path
			if fi.IsDir() {
				path = filepath.Join(w.f.path, filepath.Base(event.Name))
			}
			// Editors often truncate and rewrite; a short pause lets the
			// write finish before the file is read back.
			time.Sleep(time.Millisecond)
			kv, err := w.f.loadFile(path)
			if err != nil {
				return nil, err
			}
			return []*config.KeyValue{kv}, nil
		case err, ok := <-w.fw.Errors:
			if !ok {
				return nil, context.Canceled
			}
			return nil, err
		}
	}
}

// Stop unblocks an in-flight Next and releases the filesystem watcher. It is
// safe to call more than once.
func (w *watcher) Stop() error {
	var err error
	w.once.Do(func() {
		close(w.exit)
		err = w.fw.Close()
	})
	return err
}
