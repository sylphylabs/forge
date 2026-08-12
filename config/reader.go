package config

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sylphylabs/forge/internal/protojsonutil"
	"github.com/sylphylabs/forge/log"
)

// reader accumulates decoded source payloads into one key tree and serves
// point lookups over it. Each load builds a fresh reader; [Config] swaps the
// current one atomically on reload.
type reader struct {
	opts   options
	values map[string]any
	lock   sync.Mutex
}

func newReader(opts options) *reader {
	return &reader{
		opts:   opts,
		values: make(map[string]any),
		lock:   sync.Mutex{},
	}
}

func (r *reader) Merge(kvs ...*KeyValue) error {
	merged, err := r.cloneMap()
	if err != nil {
		return err
	}
	for _, kv := range kvs {
		next := make(map[string]any)
		if err := r.opts.decoder(kv, next); err != nil {
			log.Error("failed to decode config", "error", err, "key", kv.Key, "value", string(kv.Value))
			return err
		}
		if err := r.opts.merge(&merged, convertMap(next)); err != nil {
			log.Error("failed to merge config", "error", err, "key", kv.Key, "value", string(kv.Value))
			return err
		}
	}
	r.lock.Lock()
	r.values = merged
	r.lock.Unlock()
	return nil
}

func (r *reader) Value(path string) (*atomicValue, bool) {
	r.lock.Lock()
	defer r.lock.Unlock()
	return readValue(r.values, path)
}

func (r *reader) Source() ([]byte, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	return marshalJSON(convertMap(r.values))
}

func (r *reader) Resolve() error {
	r.lock.Lock()
	defer r.lock.Unlock()
	return r.opts.resolver(r.values)
}

// cloneMap returns a deep copy of the accumulated values.
//
// It shares the merge package's copier rather than round-tripping through gob.
// The gob encoding it replaced decoded an empty slice as nil, so a config value
// declared as `[]` became null on the second merge, and gob.Register mutated a
// process-global registry on every call.
func (r *reader) cloneMap() (map[string]any, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	return cloneMap(r.values)
}

func cloneMap(src map[string]any) (map[string]any, error) {
	cloned, _ := cloneMergeValue(src).(map[string]any)
	return cloned, nil
}

func convertMap(src any) any {
	switch m := src.(type) {
	case map[string]any:
		dst := make(map[string]any, len(m))
		for k, v := range m {
			dst[k] = convertMap(v)
		}
		return dst
	case map[any]any:
		dst := make(map[string]any, len(m))
		for k, v := range m {
			dst[fmt.Sprint(k)] = convertMap(v)
		}
		return dst
	case []any:
		dst := make([]any, len(m))
		for k, v := range m {
			dst[k] = convertMap(v)
		}
		return dst
	case []byte:
		// there will be no binary data in the config data
		return string(m)
	default:
		return src
	}
}

// readValue walks the dot-separated path through nested maps and returns the
// value at its end, reporting false when any segment is missing.
func readValue(values map[string]any, path string) (*atomicValue, bool) {
	var (
		next = values
		keys = strings.Split(path, ".")
		last = len(keys) - 1
	)
	for idx, key := range keys {
		value, ok := next[key]
		if !ok {
			return nil, false
		}
		if idx == last {
			av := &atomicValue{}
			av.Store(value)
			return av, true
		}
		switch vm := value.(type) {
		case map[string]any:
			next = vm
		default:
			return nil, false
		}
	}
	return nil, false
}

func marshalJSON(v any) ([]byte, error) {
	return protojsonutil.Marshal(v)
}

func unmarshalJSON(data []byte, v any) error {
	return protojsonutil.Unmarshal(data, v)
}
