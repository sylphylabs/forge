package file

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/openkratos/kratos/config"
)

var _ config.Watcher = (*watcher)(nil)

type watcher struct {
	f  *file
	fw *fsnotify.Watcher

	ctx    context.Context
	cancel context.CancelFunc
}

func newWatcher(f *file) (config.Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := fw.Add(f.path); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &watcher{f: f, fw: fw, ctx: ctx, cancel: cancel}, nil
}

func (w *watcher) Next() ([]*config.KeyValue, error) {
	for {
		select {
		case <-w.ctx.Done():
			return nil, w.ctx.Err()
		case event, ok := <-w.fw.Events:
			if !ok {
				return nil, w.ctx.Err()
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
			time.Sleep(time.Millisecond)
			kv, err := w.f.loadFile(path)
			if err != nil {
				return nil, err
			}
			return []*config.KeyValue{kv}, nil
		case err, ok := <-w.fw.Errors:
			if !ok {
				return nil, w.ctx.Err()
			}
			return nil, err
		}
	}
}

func (w *watcher) Stop() error {
	w.cancel()
	return w.fw.Close()
}
