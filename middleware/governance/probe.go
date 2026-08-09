package governance

import (
	"context"

	"github.com/sylphylabs/forge/diagnosis"
)

// Probe returns a probe that reports the rule snapshot currently served by
// r: every exact-match rule keyed by its operation, plus the fallback under
// the [Wildcard] key. Because [Rules.Replace] installs snapshots atomically,
// the probe always reports a complete rule set — exactly the one requests
// are being matched against at that moment, which is the question a dump
// answers when a request was limited or timed out unexpectedly.
//
// describe projects one rule value into the JSON-serializable form the
// snapshot carries, because a rule value need not be serializable itself —
// a rate-limit rule holds a live limiter. Pass nil when T already
// serializes, such as a duration or a threshold struct; the rule value is
// then reported as is.
//
// Register the returned probe under a name that identifies the governed
// middleware, "governance/ratelimit" by convention:
//
//	reg.Register("governance/ratelimit", governance.Probe(limits, describeLimiter))
//
// Probe panics if r is nil; a probe wired to no table is a construction bug,
// surfaced at the offending line rather than as an error in every dump.
func Probe[T any](r *Rules[T], describe func(T) any) diagnosis.ProbeFunc {
	if r == nil {
		panic("governance: Probe called with a nil Rules table")
	}
	if describe == nil {
		describe = func(v T) any { return v }
	}
	return func(context.Context) (any, error) {
		t := r.table.Load()
		snapshot := make(map[string]any, len(t.rules)+1)
		snapshot[Wildcard] = describe(t.fallback)
		for op, v := range t.rules {
			snapshot[op] = describe(v)
		}
		return snapshot, nil
	}
}
