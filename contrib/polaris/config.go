package polaris

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"

	"github.com/sylphylabs/forge/config"
	"github.com/sylphylabs/forge/log"
)

// ConfigOption is polaris config option.
type ConfigOption func(o *configOptions)

type configOptions struct {
	namespace  string
	files      []File
	configFile []polaris.ConfigFile
}

// WithConfigFile with polaris config file
func WithConfigFile(file ...File) ConfigOption {
	return func(o *configOptions) {
		o.files = file
	}
}

type File struct {
	Name  string
	Group string
}

type source struct {
	client  polaris.ConfigAPI
	options *configOptions
}

// Load return the config values
func (s *source) Load(context.Context) ([]*config.KeyValue, error) {
	kvs := make([]*config.KeyValue, 0, len(s.options.files))
	for _, file := range s.options.files {
		configFile, err := s.client.FetchConfigFile(&polaris.GetConfigFileRequest{
			GetConfigFileRequest: &model.GetConfigFileRequest{
				Namespace: s.options.namespace,
				FileGroup: file.Group,
				FileName:  file.Name,
				Subscribe: true,
			},
		})
		if err != nil {
			return nil, err
		}
		s.options.configFile = append(s.options.configFile, configFile)
		kvs = append(kvs, &config.KeyValue{
			Key:    file.Name,
			Value:  []byte(configFile.GetContent()),
			Format: strings.TrimPrefix(filepath.Ext(file.Name), "."),
		})
	}
	return kvs, nil
}

// Watch return the watcher
func (s *source) Watch(context.Context) (config.Watcher, error) {
	return newConfigWatcher(s.options.configFile), nil
}

type ConfigWatcher struct {
	event chan model.ConfigFileChangeEvent
	cfg   []*config.KeyValue
}

func receive(event chan model.ConfigFileChangeEvent) func(m model.ConfigFileChangeEvent) {
	return func(m model.ConfigFileChangeEvent) {
		defer func() {
			if err := recover(); err != nil {
				log.Error("panic recovered", "err", err)
			}
		}()
		event <- m
	}
}

func newConfigWatcher(configFile []polaris.ConfigFile) *ConfigWatcher {
	w := &ConfigWatcher{
		event: make(chan model.ConfigFileChangeEvent, len(configFile)),
	}
	for _, file := range configFile {
		w.cfg = append(w.cfg, &config.KeyValue{
			Key:    file.GetFileName(),
			Value:  []byte(file.GetContent()),
			Format: strings.TrimPrefix(filepath.Ext(file.GetFileName()), "."),
		})
	}
	for _, file := range configFile {
		file.AddChangeListener(receive(w.event))
	}
	return w
}

func (w *ConfigWatcher) Next(ctx context.Context) ([]*config.KeyValue, error) {
	var (
		event model.ConfigFileChangeEvent
		ok    bool
	)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case event, ok = <-w.event:
	}
	if ok {
		m := make(map[string]*config.KeyValue)
		for _, file := range w.cfg {
			m[file.Key] = file
		}
		m[event.ConfigFileMetadata.GetFileName()] = &config.KeyValue{
			Key:    event.ConfigFileMetadata.GetFileName(),
			Value:  []byte(event.NewValue),
			Format: strings.TrimPrefix(filepath.Ext(event.ConfigFileMetadata.GetFileName()), "."),
		}
		w.cfg = make([]*config.KeyValue, 0, len(m))
		for _, kv := range m {
			w.cfg = append(w.cfg, kv)
		}
		return w.cfg, nil
	}
	return nil, context.Canceled
}

func (w *ConfigWatcher) Stop() error {
	close(w.event)
	return nil
}
