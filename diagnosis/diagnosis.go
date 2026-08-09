// Package diagnosis gives components one shared way to expose internal
// state for inspection.
//
// A component registers a named [ProbeFunc] — a function that returns a
// point-in-time, JSON-serializable snapshot of whatever the component wants
// to reveal: its identity, the rule table currently in effect, connection
// counters. A diagnostic consumer — a debug endpoint, a dump tool — reads
// probes by name or all at once through [Registry.Probe] and
// [Registry.Collect] without knowing which components exist or how each one
// stores its state.
//
// The registry is an explicit value. Construct one with [NewRegistry], hand
// it to the components that report into it and to the consumers that read
// from it; two applications in one process hold two registries and share
// nothing. There is no package-level default.
//
// A probe answers "what is this component's state right now", not "is this
// component healthy": results carry data, never a verdict, and no consumer
// in this package interprets them. Liveness and readiness are a separate
// concern with different semantics — aggregation, thresholds, load-balancer
// contracts — and deliberately have no home here.
package diagnosis

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// ProbeFunc reports a component's current state as a point-in-time snapshot.
//
// The returned value must be serializable with encoding/json — a struct with
// exported fields, a map, a slice, a scalar — because generic consumers such
// as [Handler] render it that way; a consumer that cannot serialize a value
// reports the failure as that probe's error rather than asserting on its
// concrete type. The value must also be a snapshot: once returned it belongs
// to the consumer, so a probe must copy any state it would otherwise share
// with the running component.
//
// The context carries the consumer's deadline and cancellation. A probe that
// only reads in-memory state may ignore it; a probe that gathers data should
// honor it and return the context's error when interrupted.
//
// A ProbeFunc must be safe for concurrent use: consumers call it at any
// moment while the component runs. Returning an error reports that the
// snapshot could not be produced; a panic is recovered by the [Registry] and
// reported the same way, so a faulty probe degrades to an error entry
// instead of taking the consumer down.
type ProbeFunc func(ctx context.Context) (any, error)

// Result is the outcome of running one probe: a snapshot value, or the error
// that prevented one. Exactly one of the two fields is meaningful — when Err
// is non-nil, Value is always nil.
type Result struct {
	// Value is the snapshot the probe returned.
	Value any
	// Err reports why no snapshot was produced: the probe's own error, or a
	// recovered probe panic.
	Err error
}

// Registry maps probe names to probe functions.
//
// Registration typically happens while an application is wired, but the
// registry is not sealed afterwards: a component created later may register
// then, and every read observes the probes registered before it. All methods
// are safe for concurrent use.
//
// The zero value is not usable; construct with [NewRegistry].
type Registry struct {
	mu     sync.RWMutex
	probes map[string]ProbeFunc
}

// NewRegistry returns an empty probe registry.
func NewRegistry() *Registry {
	return &Registry{probes: make(map[string]ProbeFunc)}
}

// Register adds probe under name.
//
// Names are opaque non-empty strings, unique within a registry. By
// convention a component uses its package or component name, adding a
// slash-separated facet when it exposes more than one probe: "app",
// "governance/ratelimit", "pool/connections".
//
// Register panics on an empty name, a nil probe, or a name that is already
// registered. All three are wiring bugs — in particular, silently replacing
// a probe would let two independently written components shadow each other's
// state — so they fail at the offending call during construction rather
// than surfacing as a puzzling dump later.
func (r *Registry) Register(name string, probe ProbeFunc) {
	if name == "" {
		panic("diagnosis: Register called with an empty probe name")
	}
	if probe == nil {
		panic(fmt.Sprintf("diagnosis: Register called with a nil ProbeFunc for %q", name))
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.probes[name]; ok {
		panic(fmt.Sprintf("diagnosis: probe %q is already registered", name))
	}
	r.probes[name] = probe
}

// Names returns the registered probe names in lexical order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	names := make([]string, 0, len(r.probes))
	for name := range r.probes {
		names = append(names, name)
	}
	r.mu.RUnlock()
	slices.Sort(names)
	return names
}

// Probe runs the probe registered under name and returns its result. The
// second return value reports whether such a probe exists, letting a
// consumer distinguish "unknown probe" from "probe failed".
//
// The probe runs outside the registry's lock, so a slow probe never blocks
// registration or other reads, and a probe panic is recovered into the
// result's Err.
func (r *Registry) Probe(ctx context.Context, name string) (Result, bool) {
	r.mu.RLock()
	probe, ok := r.probes[name]
	r.mu.RUnlock()
	if !ok {
		return Result{}, false
	}
	return run(ctx, name, probe), true
}

// Collect runs every registered probe and returns the results keyed by probe
// name. Probes run sequentially outside the registry's lock; a probe error
// or panic occupies that probe's entry and leaves every other entry intact.
func (r *Registry) Collect(ctx context.Context) map[string]Result {
	r.mu.RLock()
	probes := make(map[string]ProbeFunc, len(r.probes))
	for name, probe := range r.probes {
		probes[name] = probe
	}
	r.mu.RUnlock()

	results := make(map[string]Result, len(probes))
	for name, probe := range probes {
		results[name] = run(ctx, name, probe)
	}
	return results
}

// run invokes one probe, converting a panic into the probe's error so that a
// faulty probe cannot take its consumer down.
func run(ctx context.Context, name string, probe ProbeFunc) (res Result) {
	defer func() {
		if v := recover(); v != nil {
			res = Result{Err: fmt.Errorf("diagnosis: probe %q panicked: %v", name, v)}
		}
	}()
	value, err := probe(ctx)
	if err != nil {
		return Result{Err: err}
	}
	return Result{Value: value}
}
