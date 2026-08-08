# Option Suites

Status: accepted core contract

Last reviewed: August 8, 2026

## Decision

Forge adds one interface and one function to the root package:

```go
type Suite interface {
	Options() []Option
}

func WithSuite(s Suite) Option
```

A Suite bundles application options that belong together — a service registrar
plus the timeout it was tuned for, a logger plus the metadata its handlers
read. The integration author implements Suite once; the application adopts the
bundle with a single `WithSuite` call in its `forge.New` option list.

The mechanism follows the suite extension in CloudWeGo Kitex. What Forge keeps
is the shape — an interface with one method returning the native option type,
expanded by a `With`-style option — and the property that shape guarantees:
a Suite is an ordinary value, so two applications can hold differently
configured instances of the same Suite type without any global state.

## Which Option Layer

Forge has three functional-option families: the application-level
`forge.Option` (`func(*options)`, [options.go](../../options.go)), and the
transport-level `http.ServerOption` and `grpc.ServerOption` in their own
packages. Suite lives only at the application level.

The application level is where the integration problem exists. A complete
integration is cross-cutting by nature: discovery is a `Registrar` plus a
`RegistrarTimeout`; observability is a `Logger` plus `Metadata` plus perhaps
lifecycle hooks. Transport options, by contrast, configure a single server —
address, TLS, codecs — and their consumers are the application authors
themselves, not third-party integrators shipping bundles.

A generic `Suite[O any]` covering all three layers was rejected on the
second-implementation test: there is no transport-level suite implementation
waiting to exist, and a generic contract would force every layer to share one
expansion mechanism before any layer besides the application has demonstrated
the need. If a transport-level bundle becomes real, `http.ServerSuite` can be
added in its own package with the same two-line shape; nothing in this
contract blocks that, and nothing in this contract pays for it in advance.

## Expansion Semantics

`WithSuite` expands in place. The returned `Option` applies the suite's
options exactly where it appears in the caller's list, in the order `Options`
returned them. Nesting therefore expands depth-first: an option produced by an
inner `WithSuite` runs its whole bundle before the outer suite's next option
applies. No flattening pass, no reordering, no deduplication.

Duplicate settings keep the semantics options already have: the option applied
last wins, whether it came from a suite or was written directly. This makes
override behavior predictable from the option list alone — a user who writes
`forge.New(WithSuite(s), forge.Name("mine"))` overrides the suite, and one who
writes the suite last accepts its values. A deduplication or conflict-error
scheme was rejected because it would give suite-provided options different
semantics from hand-written ones, and "last wins" is precisely how a user
already reasons about two hand-written options.

`WithSuite` calls `Options` once, immediately, and validates the result before
returning: a nil Suite or a nil element panics at the `WithSuite` call site.
This follows the construction-time validation stance of
`middleware.ComposeUnary`, moved one step earlier — the failure surfaces at
the offending line in the wiring code, not inside `forge.New`, and not as a
nil-dereference when the options are eventually applied. Panic rather than an
error return because `Option` construction is infallible everywhere else in
the package; making `WithSuite` alone return `(Option, error)` would poison
every call site of an API whose only failure mode is a wiring bug. The
`encoding` package establishes the same precedent for registration-time
misuse.

`Options` is deliberately read once. A suite that wants per-application
variation makes differently configured instances; it does not get a callback
per `New`.

## Naming

The name is `Suite`, kept from Kitex. Forge's `-er` convention
(`Transporter`, `Endpointer`, `ReplyHeaderer`) names an interface after the
capability its method provides, but `Optioner` names nothing a reader can
recognize, and the method here returns a bundle rather than performing a
behavior. `Bundle` and `Preset` were considered: `Bundle` describes the value
but not the extension-point role, and `Preset` wrongly suggests defaults that
the user is expected to override. `Suite` is the term integrators arriving
from the wider Go microservice ecosystem already know, and adopting the
established name for the established mechanism costs nothing in clarity.

## What a Suite May Not Do

Suite adds no dependency stance. The interface mentions only `Option`; it
pulls in no registry, no config source, no middleware. A concrete suite in a
contrib module carries its own dependencies, and an application that never
calls `WithSuite` pays nothing.
