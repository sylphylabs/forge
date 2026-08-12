// Package env sources configuration from process environment variables,
// optionally filtered and trimmed by prefix.
package env

import (
	"context"
	"os"
	"strings"

	"github.com/sylphylabs/forge/config"
)

var _ config.Source = (*env)(nil)

type env struct {
	prefixes []string
}

// NewSource returns a source that loads environment variables. With no
// prefixes every variable is loaded under its own name; with prefixes, only
// matching variables are loaded, keyed by the name with the prefix (and one
// separating underscore) removed.
func NewSource(prefixes ...string) config.Source {
	return &env{prefixes: prefixes}
}

func (e *env) Load(context.Context) ([]*config.KeyValue, error) {
	return e.load(os.Environ()), nil
}

func (e *env) load(envs []string) []*config.KeyValue {
	var kvs []*config.KeyValue
	for _, env := range envs {
		k, v, _ := strings.Cut(env, "=")
		if k == "" {
			continue
		}
		if len(e.prefixes) > 0 {
			prefix, ok := matchPrefix(e.prefixes, k)
			if !ok || k == prefix {
				continue
			}
			k = strings.TrimPrefix(k, prefix)
			k = strings.TrimPrefix(k, "_")
		}
		if k != "" {
			kvs = append(kvs, &config.KeyValue{
				Key:   k,
				Value: []byte(v),
			})
		}
	}
	return kvs
}

func (e *env) Watch(context.Context) (config.Watcher, error) {
	return newWatcher(), nil
}

func matchPrefix(prefixes []string, s string) (string, bool) {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return p, true
		}
	}
	return "", false
}
