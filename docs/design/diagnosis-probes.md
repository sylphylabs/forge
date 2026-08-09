# Diagnosis Probes

Status: implemented

Last reviewed: August 9, 2026

## Purpose

This document defines Forge's runtime-introspection extension point: how a
component exposes its internal state for inspection, how a diagnostic
consumer reads it, and where the registry that connects the two lives. It
covers the `diagnosis` package, the probe implementations in the root and
`middleware/governance` packages, and the HTTP debug handler.

Health checking is out of scope. A probe answers "what is this component's
state right now"; liveness and readiness answer "should traffic come here",
which involves aggregation, thresholds, and load-balancer contracts that
probes deliberately do not have. Probe results carry data, never a verdict.

## Decision

Forge adds one small package with one central type:

```go
// diagnosis
type ProbeFunc func(ctx context.Context) (any, error)

type Result struct {
    Value any
    Err   error
}

type Registry struct{ ... }
func NewRegistry() *Registry
func (r *Registry) Register(name string, probe ProbeFunc)      // panics on misuse
func (r *Registry) Names() []string                            // sorted
func (r *Registry) Probe(ctx context.Context, name string) (Result, bool)
func (r *Registry) Collect(ctx context.Context) map[string]Result

func NewHandler(r *Registry) http.Handler                      // debug consumer
```

A component that wants to be inspectable registers a named function that
returns a point-in-time, JSON-serializable snapshot of its state. A consumer
— a debug endpoint, a dump tool, an operator's one-off script — reads one
probe by name or collects all of them, without knowing which components
exist or how each stores its state. The mechanism is the diagnosis extension
in CloudWeGo Kitex reduced to its useful core: named snapshot functions
behind a uniform pull interface, with Forge-native failure semantics and no
service object.

Two probe constructors ship with the mechanism:

```go
// root package
func AppProbe(info AppInfo) diagnosis.ProbeFunc          // identity + endpoints

// middleware/governance
func Probe[T any](r *Rules[T], describe func(T) any) diagnosis.ProbeFunc
```

## Package Placement

`diagnosis` is a new top-level package that imports only the standard
library. The import direction is the reason: probe providers are everywhere
— the root package, middleware, transports, contrib modules — so anything
the registry package imported would be pulled into all of them, and any
Forge package it depended on could never register a probe without an import
cycle. The root package already imports `transport` and `registry`;
`middleware/governance` sits under `middleware`. A registry living in either
place would be unusable from the other side. The same constraint shaped
`encoding` and `log`, which sit at the top level and depend on almost
nothing; `diagnosis` follows them.

The probe implementations live with the state they expose, not in
`diagnosis`: `AppProbe` in the root package next to `AppInfo`, and
`governance.Probe` next to `Rules`. The registry package stays free of
knowledge about any particular component, so a contrib module can register
probes on exactly the same footing as the core.

## How the Registry Instance Flows

The registry is an ordinary value: `NewRegistry` constructs one, and the
application hands it to the components that report into it and the consumers
that read from it. There is no package-level default registry, no `Option`
that stores a registry inside `App`, and no context key.

A package-level default was rejected outright: it is shared mutable state
between every application in a process, makes probe names collide across
tests, and cannot be replaced by a caller who wants two isolated registries.
It fails on the same grounds as go-micro's `DefaultRegistry`, and it would
contradict `Suite`'s documented property that integrations carry no global
state.

An `App` option (`forge.Diagnosis(reg)`) was rejected because it buys
nothing the plain value does not already provide, and it would obligate
`App` to define what it does with the registry — mount it? expose it? —
decisions this design deliberately leaves with the application. `App`
manages lifecycle; it is not a service locator, and the one accessor it
exports (`AppInfo` via context) exists for lifecycle hooks, not for
component wiring.

Context flow was rejected as the primary mechanism because registration
happens at wiring time, when the interesting contexts do not exist yet;
threading a registry through `context.Context` to reach construction code is
the pattern the ecosystem warns against (values, not dependencies, belong in
contexts).

The plain value composes with what exists instead. An integration that wants
to bundle probes with its other options takes the registry in its
constructor and registers through a lifecycle hook — the suite test in
[probe_test.go](../../probe_test.go) shows a `Suite` whose `AfterStart`
option pulls the `AppInfo` from the hook context and registers an identity
probe. No new extension point was needed, which is the point: `Suite`
bundles options, options give lifecycle hooks, hooks see the app; a registry
that is a value slots into that chain as-is.

## Probe Contract

`ProbeFunc` is `func(context.Context) (any, error)`.

The return type is `any` with a documented obligation, not an enforced
marshaling interface. Requiring `json.Marshaler`, or marshaling eagerly to
`[]byte` inside the registry, was rejected: it would force every probe to
pay serialization cost even for in-process consumers that want the typed
value (the tests assert on `AppSnapshot` fields directly), and it would
couple the registry to one encoding. The error is still moved forward as far
as it can usefully go — `NewHandler` folds an unmarshalable value into that
probe's error entry rather than failing the response, so a contract
violation surfaces as a labeled error at the first dump, in the probe's own
entry, not as a broken endpoint. Kitex's `func(request interface{})
interface{}` shape was not kept: the request parameter has no defined
meaning ("reserved"), and a probe with no error channel can only smuggle
failures through the value. Forge probes take a context (deadline,
cancellation for probes that gather) and return an explicit error.

The value must be a snapshot: once returned it belongs to the consumer, so a
probe copies anything it would otherwise share with the running component.
`AppProbe` clones metadata and endpoints; `governance.Probe` builds a fresh
map per call from the atomically loaded table.

## Naming and Registration Semantics

Names are opaque non-empty strings, unique per registry. Convention:
component name, plus a slash-separated facet when one component exposes
several probes (`app`, `governance/ratelimit`).

Duplicate registration panics. Overwrite — what `encoding.RegisterCodec`
does — is wrong here because codec names are a closed negotiation vocabulary
where replacing "json" is a feature, while probe names are contributed by
independently written components that must not silently shadow each other's
state: a dump that quietly shows the wrong component's data is the worst
failure mode a diagnostic tool can have. An error return was rejected for
the reason established by `WithSuite`: registration has no legitimate
runtime failure, only wiring bugs, and an error return would poison every
registration site with handling for a condition that is always a programming
mistake. Panic at the offending line during construction is the established
posture (`WithSuite`, `RegisterCodec`'s nil checks), and both probe
constructors (`AppProbe(nil)`, `governance.Probe(nil, ...)`) panic on the
same grounds.

## Concurrency

Registration is not sealed at startup: a component created later — a lazily
opened pool, a connection established mid-run — registers when it exists,
and every read observes the probes registered before it. The registry
therefore synchronizes with a `sync.RWMutex` rather than the
`atomic.Pointer` snapshot style of `governance.Rules`. The two choices fit
their read paths: `Rules.For` sits on every request and must be wait-free;
registry reads happen when a human asks for a dump, so an RWMutex's
uncontended read lock is beyond sufficient, and copy-on-write would make the
common operation (registration) allocate a full map copy to optimize the
rare one.

Probes always run outside the registry's lock — `Probe` and `Collect` copy
the function references out under the read lock, then call them — so a slow
or blocked probe never prevents registration or other reads (tested in
`TestSlowProbeDoesNotBlockRegistry`).

## Failure Semantics

A faulty probe degrades to an error entry; it never takes the consumer down.
`Registry.Probe` and `Registry.Collect` recover a probe panic into that
probe's `Result.Err`, labeled with the probe name, and `Collect` leaves
every other entry intact. `Probe`'s second return value distinguishes
"unknown probe" from "probe failed", so a consumer can 404 the former and
500 the latter. This is the recovery middleware's posture applied to
diagnostics: introspection exists precisely for when a component misbehaves,
so the inspection path must survive what it inspects.

## Shipped Probes

- **Application identity** (`forge.AppProbe`): id, name, version, metadata,
  and currently advertised endpoints, read live from an `AppInfo` at probe
  time — a snapshot taken after startup includes the listener-resolved
  endpoints. This is the "which instance am I even talking to" probe.
- **Governance rule table** (`governance.Probe`): the complete rule snapshot
  a `Rules[T]` currently serves — every exact-match rule plus the fallback
  under the `*` key, atomically consistent because `Replace` installs whole
  snapshots. This answers "why was this request limited/timed out" with the
  rules actually in effect, not the ones in the config file. The `describe`
  parameter projects rule values that are not serializable themselves (a
  rate-limit rule holds a live limiter) into reportable form; `nil` reports
  values as-is for self-serializing rules such as timeout durations.

## The Debug Handler

`diagnosis.NewHandler` is the consumer shipped with the mechanism: a plain
`net/http.Handler` that serves a registry as JSON — `GET /` for a full dump
(status 200 even when probes fail; a dump reports state, it does not judge
it), `GET /<name>` for one probe (404 unknown, 500 failed). Object keys are
emitted in lexical order so two dumps of identical state are byte-identical
and diffable.

It is a handler, not a server. It opens no port, starts no goroutine, and
registers no route; the application mounts it wherever and however it wants:

```go
mux.Handle("/debug/probes/", http.StripPrefix("/debug/probes", diagnosis.NewHandler(reg)))
```

Exposure is a security decision — a probe dump reveals internals by design —
so which listener carries it and what authentication guards it must remain
the application's choice. Auto-mounting into `transport/http` was rejected
for that reason and for coupling: the handler uses only `net/http`, so it
works with Forge's HTTP transport, a private ops listener, or no Forge
transport at all.

## Alternatives Rejected

- **A package-level default registry.** See "How the Registry Instance
  Flows"; the go-micro `DefaultRegistry` failure mode.
- **A `Service` interface à la Kitex** (`RegisterProbeFunc(ProbeName,
  ProbeFunc)` implemented by the framework object). Rejected: it welds the
  registry to one owner. As a free-standing value the registry serves
  applications that do not use `forge.App` at all, and tests construct one
  per case with no framework scaffolding.
- **Requiring `json.Marshaler` or eager serialization.** See "Probe
  Contract".
- **Overwrite or error-return on duplicate names.** See "Naming and
  Registration Semantics".
- **Sealing the registry after startup** (build-then-freeze, atomic
  snapshot). Rejected: late-created components are real, and the wait-free
  read path that justifies copy-on-write in `governance.Rules` has no
  analogue on a human-paced dump endpoint.
- **Auto-registering built-in probes** (every `forge.New` app implicitly
  probed). Rejected: it requires the app to hold a registry, recreating the
  option coupling above, and makes exposure opt-out instead of opt-in.
  Wiring `AppProbe` is one line.
- **Health semantics** — per-probe status fields, aggregate verdicts,
  `healthz` conventions. Out of scope by design; see Purpose.
- **New dependencies.** None added; the package uses the standard library
  only, and `go.mod` is unchanged.
