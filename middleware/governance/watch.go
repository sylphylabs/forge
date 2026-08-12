package governance

import (
	"fmt"

	"github.com/sylphylabs/forge/config"
	"github.com/sylphylabs/forge/log"
)

// ParseFunc builds one rule of type T from its raw config value. It is the
// validation boundary: implementations must reject values that would be
// unsafe to serve — a negative threshold, a malformed duration — by returning
// an error, and must not repair them into defaults.
type ParseFunc[T any] func(config.Value) (T, error)

// Watch feeds r from the config section at key and keeps it fed: every time
// the section changes, the new snapshot is parsed, validated, and installed
// atomically.
//
// The section must be a map from operation string to rule value, with the
// optional [Wildcard] entry as the fallback rule. Operation strings are used
// verbatim as map keys and never interpreted as config paths, so operations
// containing dots are safe. The section must exist when Watch is called;
// Watch returns an error otherwise, and it returns any error from parsing the
// initial snapshot so that a service never starts against rules it cannot
// honor.
//
// After a successful return, failures are conservative: if any rule in a
// later snapshot fails to parse, the entire snapshot is rejected, the
// previously installed rules stay in effect, and the rejection is logged.
// A dynamic update never partially applies and never silently downgrades a
// rule to a zero value.
//
// Each call adds one observer on key; a [config.Config] supports several
// observers per key, but give every watched rule table its own section so
// unrelated tables never alias one another's rules.
func Watch[T any](c *config.Config, key string, r *Rules[T], parse ParseFunc[T]) error {
	if c == nil || r == nil || parse == nil {
		return fmt.Errorf("governance: Watch requires a config, a rule table, and a parse function")
	}
	if err := apply(c.Value(key), r, parse); err != nil {
		return fmt.Errorf("governance: initial rules at %q: %w", key, err)
	}
	return c.Watch(key, func(k string, v config.Value) {
		if err := apply(v, r, parse); err != nil {
			log.Error("governance: rejected rules update, keeping previous rules", "key", k, "error", err)
		}
	})
}

// apply parses a full section snapshot and installs it, or installs nothing.
func apply[T any](v config.Value, r *Rules[T], parse ParseFunc[T]) error {
	section, err := v.Map()
	if err != nil {
		return fmt.Errorf("rules section: %w", err)
	}
	rules := make(map[string]T, len(section))
	for op, rv := range section {
		rule, err := parse(rv)
		if err != nil {
			return fmt.Errorf("rule %q: %w", op, err)
		}
		rules[op] = rule
	}
	r.Replace(rules)
	return nil
}
