// Package governance turns middleware parameters into dynamically observable
// values instead of construction-time constants.
//
// The package contributes exactly one concept: [Rules], an atomic,
// operation-keyed rule table. A middleware accepts a *Rules[T] where it used
// to accept a bare T and reads it on every call; [Watch] feeds the table from
// a [config.Config] section so that rule changes published by any config
// source take effect without a restart.
//
// Operations are matched by exact string comparison, with the [Wildcard] key
// as the fallback. The operation string reported by a transport is opaque:
// this package never parses it, and rule keys carry no pattern syntax beyond
// the single reserved [Wildcard] key.
package governance

import "sync/atomic"

// Wildcard is the reserved rule key that applies to every operation without
// an exact-match rule. It is compared literally; no other key carries pattern
// semantics.
const Wildcard = "*"

// Rules is a concurrency-safe, operation-keyed rule table.
//
// Reads are wait-free: [Rules.For] performs one atomic pointer load and one
// map lookup, so it is safe and cheap to call on every request. Updates
// replace the whole table atomically via [Rules.Replace]; readers always
// observe a complete snapshot, never a partially applied one.
//
// The zero value is not usable; construct with [NewRules].
type Rules[T any] struct {
	def   T
	table atomic.Pointer[table[T]]
}

type table[T any] struct {
	fallback T
	rules    map[string]T
}

// NewRules returns a rule table whose every lookup yields def until a
// snapshot is installed with [Rules.Replace] or [Watch].
//
// def is the construction-time default. It backs the [Wildcard] slot of every
// snapshot that does not set one itself, so removing rules from configuration
// restores the behavior the middleware was constructed with rather than a
// zero value.
func NewRules[T any](def T) *Rules[T] {
	r := &Rules[T]{def: def}
	r.table.Store(&table[T]{fallback: def})
	return r
}

// For returns the rule in effect for operation: the exact-match rule if the
// current snapshot has one, otherwise the snapshot's [Wildcard] rule,
// otherwise the construction-time default.
//
// The operation string is treated as an opaque key. An empty operation — a
// call outside any transport context — simply finds no exact match and
// receives the fallback.
func (r *Rules[T]) For(operation string) T {
	t := r.table.Load()
	if v, ok := t.rules[operation]; ok {
		return v
	}
	return t.fallback
}

// Replace atomically installs rules as the complete new snapshot, discarding
// the previous one. The [Wildcard] entry, if present, becomes the fallback
// for unmatched operations; without one the construction-time default is the
// fallback. A nil or empty map resets the table to defaults.
//
// Replace performs no validation. Callers that accept rules from an external
// source must validate before installing; [Watch] does so via its build
// function and refuses invalid snapshots wholesale.
func (r *Rules[T]) Replace(rules map[string]T) {
	t := &table[T]{fallback: r.def}
	if len(rules) > 0 {
		t.rules = make(map[string]T, len(rules))
		for op, v := range rules {
			if op == Wildcard {
				t.fallback = v
				continue
			}
			t.rules[op] = v
		}
	}
	r.table.Store(t)
}
