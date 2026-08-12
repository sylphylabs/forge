// Package file sources configuration from a file or every non-hidden file in
// a directory, inferring each payload's format from the file extension.
package file

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sylphylabs/forge/config"
)

var _ config.Source = (*file)(nil)

type file struct {
	path string
}

// NewSource returns a source that loads path: one file, or every non-hidden
// file in the directory it names.
func NewSource(path string) config.Source {
	return &file{path: path}
}

func (f *file) loadFile(path string) (*config.KeyValue, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return &config.KeyValue{
		Key:    info.Name(),
		Format: format(info.Name()),
		Value:  data,
	}, nil
}

func (f *file) loadDir(path string) (kvs []*config.KeyValue, err error) {
	files, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	for _, file := range files {
		// ignore hidden files
		if file.IsDir() || skipFile(file.Name()) {
			continue
		}
		kv, err := f.loadFile(filepath.Join(path, file.Name()))
		if err != nil {
			return nil, err
		}
		kvs = append(kvs, kv)
	}
	return
}

func skipFile(name string) bool {
	return strings.HasPrefix(name, ".")
}

func (f *file) Load(context.Context) (kvs []*config.KeyValue, err error) {
	fi, err := os.Stat(f.path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return f.loadDir(f.path)
	}
	kv, err := f.loadFile(f.path)
	if err != nil {
		return nil, err
	}
	return []*config.KeyValue{kv}, nil
}

func (f *file) Watch(context.Context) (config.Watcher, error) {
	return newWatcher(f)
}
