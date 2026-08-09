package config

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	// init encoding
	_ "github.com/sylphylabs/forge/encoding/json"
	_ "github.com/sylphylabs/forge/encoding/proto"
	_ "github.com/sylphylabs/forge/encoding/xml"
	_ "github.com/sylphylabs/forge/encoding/yaml"
	"github.com/sylphylabs/forge/log"
)

var _ Config = (*config)(nil)

var ErrNotFound = errors.New("key not found") // ErrNotFound is key not found.

// Observer is config observer.
type Observer func(string, Value)

// Config is a config interface.
type Config interface {
	Load() error
	Scan(v any) error
	Value(key string) Value
	Watch(key string, o Observer) error
	Close() error
}

type config struct {
	opts      options
	reader    atomic.Pointer[reader]
	reloadMu  sync.Mutex
	cached    sync.Map
	observers sync.Map
	watchers  []Watcher
}

// New a config with options.
func New(opts ...Option) Config {
	o := options{
		decoder:  defaultDecoder,
		resolver: defaultResolver,
		merge:    defaultMerge,
	}
	for _, opt := range opts {
		opt(&o)
	}
	c := &config{opts: o}
	c.reader.Store(newReader(o))
	return c
}

func (c *config) watch(w Watcher) {
	for {
		_, err := w.Next()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				log.Info("watcher's ctx cancel", "error", err)
				return
			}
			time.Sleep(time.Second)
			log.Error("failed to watch next config", "error", err)
			continue
		}
		if err := c.reload(); err != nil {
			log.Error("failed to reload config", "error", err)
			continue
		}
	}
}

func (c *config) reload() error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	r, err := loadReader(c.opts)
	if err != nil {
		return err
	}
	c.reader.Store(r)
	c.updateCached(r)
	return nil
}

func (c *config) updateCached(r Reader) {
	c.cached.Range(func(key, value any) bool {
		k := key.(string)
		v := value.(Value)
		if n, ok := r.Value(k); ok && reflect.TypeOf(n.Load()) == reflect.TypeOf(v.Load()) && !reflect.DeepEqual(n.Load(), v.Load()) {
			v.Store(n.Load())
			if o, ok := c.observers.Load(k); ok {
				o.(Observer)(k, v)
			}
		}
		return true
	})
}

func (c *config) Load() error {
	r, err := loadReader(c.opts)
	if err != nil {
		return err
	}
	c.reader.Store(r)
	for _, src := range c.opts.sources {
		w, err := src.Watch()
		if err != nil {
			log.Error("failed to watch config source", "error", err)
			return err
		}
		c.watchers = append(c.watchers, w)
		go c.watch(w)
	}
	return nil
}

func loadReader(opts options) (*reader, error) {
	r := newReader(opts)
	for _, src := range opts.sources {
		kvs, err := src.Load()
		if err != nil {
			return nil, fmt.Errorf("load config source: %w", err)
		}
		for _, v := range kvs {
			log.Debug("config loaded", "key", v.Key, "format", v.Format)
		}
		if err := r.Merge(kvs...); err != nil {
			return nil, fmt.Errorf("merge config source: %w", err)
		}
	}
	if err := r.Resolve(); err != nil {
		return nil, fmt.Errorf("resolve config source: %w", err)
	}
	return r, nil
}

func (c *config) Value(key string) Value {
	if v, ok := c.cached.Load(key); ok {
		return v.(Value)
	}
	if v, ok := c.reader.Load().Value(key); ok {
		c.cached.Store(key, v)
		return v
	}
	return &errValue{err: ErrNotFound}
}

func (c *config) Scan(v any) error {
	data, err := c.reader.Load().Source()
	if err != nil {
		return err
	}
	return unmarshalJSON(data, v)
}

func (c *config) Watch(key string, o Observer) error {
	if v := c.Value(key); v.Load() == nil {
		return ErrNotFound
	}
	c.observers.Store(key, o)
	return nil
}

func (c *config) Close() error {
	for _, w := range c.watchers {
		if err := w.Stop(); err != nil {
			return err
		}
	}
	return nil
}

// Get retrieves a config value by key and scans it into the target type.
func Get[T any](c Config, key string) (T, error) {
	var t T
	v := c.Value(key)

	if v.Load() == nil {
		return t, ErrNotFound
	}

	switch any(t).(type) {
	case bool:
		b, err := v.Bool()
		return any(b).(T), err
	case int64:
		i, err := v.Int()
		return any(i).(T), err
	case int:
		i, err := v.Int()
		if err != nil {
			return t, err
		}
		if i < math.MinInt || i > math.MaxInt {
			return t, fmt.Errorf("config value %q (%d) overflows int", key, i)
		}
		return any(int(i)).(T), nil
	case float64:
		f, err := v.Float()
		return any(f).(T), err
	case string:
		s, err := v.String()
		return any(s).(T), err
	}

	err := v.Scan(&t)
	return t, err
}
