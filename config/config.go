// Package config loads configuration from pluggable sources, merges the
// results into one key tree, and keeps that tree current as sources change.
//
// A [Source] supplies raw payloads — a file, the process environment, a
// remote config service — and a [Watcher] reports when they change. [New]
// loads every source, resolves placeholders, and starts one watch loop per
// source; from then on [Config.Value], [Config.Scan], and [Get] read the
// current snapshot, and observers registered with [Config.Watch] are told
// when the value under their key changes.
package config

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	// The default decoder resolves formats through the encoding registry, so
	// the codecs config files commonly use must be linked in.
	_ "github.com/sylphylabs/forge/encoding/json"
	_ "github.com/sylphylabs/forge/encoding/proto"
	_ "github.com/sylphylabs/forge/encoding/xml"
	_ "github.com/sylphylabs/forge/encoding/yaml"
	"github.com/sylphylabs/forge/log"
)

// ErrNotFound reports that no value exists under the requested key. Config
// lookups never leave the process, so a stdlib sentinel suffices; match it
// with [errors.Is].
var ErrNotFound = errors.New("config: key not found")

// Observer is called after the value under a watched key changes. It receives
// the key and the new value.
//
// Observers run on the coordinator's watch goroutine, one at a time, so an
// observer must not block for long and must not call [Config.Watch] or
// [Config.Close] from inside itself.
type Observer func(key string, value Value)

// Config is a merged view over one or more configuration sources.
//
// The zero value is not usable; construct with [New], which loads every
// source before returning. All methods are safe for concurrent use.
type Config struct {
	opts     options
	reader   atomic.Pointer[reader]
	reloadMu sync.Mutex
	cached   sync.Map // key -> *atomicValue

	obsMu     sync.Mutex
	observers map[string][]Observer

	watchers  []Watcher
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// New loads every configured source and returns a ready Config.
//
// Construction is loading: when New returns without error the snapshot is
// complete, resolved, and being kept current by one watch loop per source, so
// no half-initialized Config ever exists. If any source fails to load or to
// establish its watcher, New stops the watchers it already constructed and
// returns the error.
//
// ctx bounds construction only — the initial loads and watcher setup. The
// watch loops outlive it and run until [Config.Close].
func New(ctx context.Context, opts ...Option) (*Config, error) {
	o := options{
		decoder:  defaultDecoder,
		resolver: defaultResolver,
		merge:    defaultMerge,
	}
	for _, opt := range opts {
		opt(&o)
	}
	c := &Config{opts: o, observers: make(map[string][]Observer)}
	r, err := loadReader(ctx, o)
	if err != nil {
		return nil, err
	}
	c.reader.Store(r)

	for _, src := range o.sources {
		w, err := src.Watch(ctx)
		if err != nil {
			errs := []error{fmt.Errorf("config: watch source: %w", err)}
			for i := len(c.watchers) - 1; i >= 0; i-- {
				if stopErr := c.watchers[i].Stop(); stopErr != nil {
					errs = append(errs, stopErr)
				}
			}
			return nil, errors.Join(errs...)
		}
		c.watchers = append(c.watchers, w)
	}

	// The watch loops serve the Config's lifetime, not the construction
	// call, so they detach from ctx's cancellation and end at Close.
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	c.cancel = cancel
	for _, w := range c.watchers {
		c.wg.Add(1)
		go c.watch(watchCtx, w)
	}
	return c, nil
}

// watch drives one source watcher: every change reloads the full snapshot,
// and a watcher error backs off before retrying so a persistently failing
// source cannot spin the loop.
func (c *Config) watch(ctx context.Context, w Watcher) {
	defer c.wg.Done()
	for {
		if _, err := w.Next(ctx); err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			log.Error("failed to watch next config", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		if ctx.Err() != nil {
			return
		}
		if err := c.reload(ctx); err != nil {
			log.Error("failed to reload config", "error", err)
		}
	}
}

// reload rebuilds the snapshot from every source and publishes it, updating
// cached values and notifying their observers. Cross-source placeholder
// references make the full rebuild necessary: a change in one source can
// alter resolved values that textually live in another.
func (c *Config) reload(ctx context.Context) error {
	c.reloadMu.Lock()
	defer c.reloadMu.Unlock()

	r, err := loadReader(ctx, c.opts)
	if err != nil {
		return err
	}
	c.reader.Store(r)
	c.updateCached(r)
	return nil
}

// updateCached refreshes every previously handed-out value from the new
// snapshot, so a Value a caller retained keeps reading current data, and
// notifies the observers of each key whose value changed.
func (c *Config) updateCached(r *reader) {
	c.cached.Range(func(key, value any) bool {
		k := key.(string)
		v := value.(*atomicValue)
		if n, ok := r.Value(k); ok && reflect.TypeOf(n.Load()) == reflect.TypeOf(v.Load()) && !reflect.DeepEqual(n.Load(), v.Load()) {
			v.Store(n.Load())
			c.notify(k, v)
		}
		return true
	})
}

// notify invokes every observer registered for key with the new value.
func (c *Config) notify(key string, v Value) {
	c.obsMu.Lock()
	observers := slices.Clone(c.observers[key])
	c.obsMu.Unlock()
	for _, o := range observers {
		o(key, v)
	}
}

// loadReader builds a fully merged and resolved snapshot from every source.
func loadReader(ctx context.Context, opts options) (*reader, error) {
	r := newReader(opts)
	for _, src := range opts.sources {
		kvs, err := src.Load(ctx)
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

// Value returns the value under key in the current snapshot. When no such
// key exists, the returned Value yields [ErrNotFound] from every accessor.
//
// A returned Value stays current: after a source change reloads the
// snapshot, it reads the new data.
func (c *Config) Value(key string) Value {
	if v, ok := c.cached.Load(key); ok {
		return v.(*atomicValue)
	}
	if v, ok := c.reader.Load().Value(key); ok {
		c.cached.Store(key, v)
		return v
	}
	return errValue{err: ErrNotFound}
}

// Scan unmarshals the whole current snapshot into v, following encoding/json
// semantics and additionally understanding Protobuf messages.
func (c *Config) Scan(v any) error {
	data, err := c.reader.Load().Source()
	if err != nil {
		return err
	}
	return unmarshalJSON(data, v)
}

// Watch registers o to be called whenever the value under key changes. A key
// accepts any number of observers; each registration adds one, and every
// registered observer sees every subsequent change. Watch returns
// [ErrNotFound] when the key does not exist in the current snapshot, so a
// typo in a watched key fails at registration instead of staying silent
// forever.
//
// Watch panics on a nil observer: that is a wiring bug at the registration
// site, and failing there beats a nil-function panic on the watch goroutine
// at some later config change. The same reasoning governs
// [github.com/sylphylabs/forge/diagnosis.Registry.Register].
func (c *Config) Watch(key string, o Observer) error {
	if o == nil {
		panic(fmt.Sprintf("config: Watch called with a nil Observer for %q", key))
	}
	if _, missing := c.Value(key).(errValue); missing {
		return ErrNotFound
	}
	c.obsMu.Lock()
	c.observers[key] = append(c.observers[key], o)
	c.obsMu.Unlock()
	return nil
}

// Close stops every source watcher and waits for the watch loops to return.
// It is idempotent and safe for concurrent use; every call reports the
// outcome of the first.
func (c *Config) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		var errs []error
		for i := len(c.watchers) - 1; i >= 0; i-- {
			if err := c.watchers[i].Stop(); err != nil {
				errs = append(errs, err)
			}
		}
		c.wg.Wait()
		c.closeErr = errors.Join(errs...)
	})
	return c.closeErr
}

// Get retrieves the value under key converted to T. Scalar types use the
// [Value] accessor for their kind; any other type is populated via
// [Value.Scan]. It returns [ErrNotFound] when the key does not exist.
func Get[T any](c *Config, key string) (T, error) {
	var t T
	v := c.Value(key)

	if ev, missing := v.(errValue); missing {
		return t, ev.err
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
