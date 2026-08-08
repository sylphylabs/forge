# Dynamic Governance Rules

Status: implemented

Last reviewed: August 9, 2026

## Purpose

This document defines how middleware parameters — rate-limit tunables,
request deadlines — follow configuration changes at runtime instead of being
fixed at construction. It covers the `middleware/governance` rule table, the
config key shape, per-operation matching, the failure semantics of dynamic
updates, and the retrofits of the rate-limit and timeout middleware.

The config watch pipeline this builds on is defined in
[`config-lifecycle.md`](config-lifecycle.md). Generated per-method middleware
wiring is defined in [`generated-middleware.md`](generated-middleware.md).

## Decision

Forge already has a complete dynamic configuration pipeline: `config.Source`
loads, `config.Watcher` signals, the coordinator reloads and notifies
`config.Observer` callbacks with typed `config.Value` snapshots. What it
lacked was a way for middleware to consume that pipeline: every middleware
froze its parameters inside its options struct when the chain was composed.

The gap is closed with one new concept, not a new pipeline:

```go
// middleware/governance
const Wildcard = "*"

type Rules[T any] struct{ ... }                     // atomic rule table
func NewRules[T any](def T) *Rules[T]
func (r *Rules[T]) For(operation string) T          // wait-free read
func (r *Rules[T]) Replace(rules map[string]T)      // atomic snapshot swap

type ParseFunc[T any] func(config.Value) (T, error) // validation boundary
func Watch[T any](c config.Config, key string, r *Rules[T], parse ParseFunc[T]) error
```

A middleware that wants a governable parameter accepts a `*Rules[T]` where it
used to accept a bare `T` and resolves it per call:

```go
srv := ratelimit.Server(ratelimit.WithRules(limits))
```

`governance.Watch` connects the table to a config section and keeps it
current. Nothing else is new: sources, watchers, observers, and value
decoding are the existing config machinery, so every source that speaks the
`config.Source` contract — file, env, or any config-center provider in
contrib — can carry governance rules with no provider-specific code.

## Config Key Shape

A governed parameter is one config section: a map from operation string to
rule value, with the reserved key `*` as the fallback rule.

```yaml
governance:
  ratelimit:
    "*":
      cpu_threshold: 800
    /helloworld.Greeter/SayHello:
      cpu_threshold: 900
      window: 5s
      bucket: 50
  timeout:
    "*": 1s
    /helloworld.Greeter/SayHello: 300ms
```

This is a plain nested map. Every config source can express it; nothing about
it is specific to a configuration center, and the `governance.` prefix is a
convention, not a requirement — `Watch` accepts any key.

The rule value shape belongs to each middleware, not to the governance
package: the rate-limit middleware defines `ratelimit.Rule` and
`ratelimit.ParseRule`, the timeout middleware reads a single duration string
via `timeout.ParseRule`. `Rules[T]` never inspects `T`.

## Operation Matching

`transport.Transporter.Operation` is documented as an opaque identifier:
callers must not parse it, because its format belongs to the transport. The
rule table honors that contract literally:

- lookup is exact string comparison against rule keys;
- the single reserved key `*` is the fallback for operations with no exact
  rule, compared literally as a whole key — it is not a pattern language;
- a table without a `*` rule falls back to the construction-time default
  passed to `NewRules`.

Operation strings are used verbatim as map keys on the read side. On the
write side, `Watch` reads the section with `Value.Map()` and iterates its
entries; operation strings containing dots (`/helloworld.Greeter/SayHello`)
are never re-split as config paths.

Matching happens inside the middleware at request time, not in a wrapper
layer: the middleware resolves its own transport context (`ratelimit` and
`timeout` read the server context), so the same mechanism works wherever the
middleware runs — hand-composed chains, generated per-method plans, or
selector-scoped chains. A request outside any transport context resolves the
empty operation and receives the fallback rule.

## Failure Semantics

Governance parameters steer live traffic, so the failure posture is
conservative in both directions:

- **Startup fails loudly.** `Watch` returns an error if the section is
  missing or the initial snapshot does not parse. A missing section is a
  wiring mistake, not an empty rule set; a service never starts against rules
  it cannot honor.
- **Updates fail closed.** After startup, a snapshot in which any rule fails
  to parse is rejected wholesale: the previously installed rules stay in
  effect and the rejection is logged with the offending rule key. There is no
  partial application and no silent downgrade to zero values.
- **Validation lives in `ParseFunc`.** The parse function is required to
  reject values that would be unsafe to serve (negative thresholds,
  non-positive deadlines) rather than repair them. `Rules.Replace` itself
  performs no validation, keeping the table generic; `Watch` never installs
  what `ParseFunc` refused.
- **Reads never block or panic.** `For` is one atomic pointer load and one
  map lookup. Readers always observe a complete snapshot — either the old
  table or the new one, never a mix.

One consequence is deliberate: because the whole section is one snapshot, one
malformed rule freezes updates to the other rules in the same section until
it is fixed. That is the safer trade — accepting the valid remainder would
make the effective rule set depend on update history rather than on the
current configuration document.

## First and Second Implementations

Two middleware consume the mechanism, exercising two different rule shapes:

- **Rate limit** (`middleware/ratelimit`): `WithRules(*governance.Rules[Limiter])`
  selects the limiter per operation per request; `ParseRule` builds a BBR
  limiter from `{window, bucket, cpu_threshold}` and rejects out-of-range
  values. The per-message stream limiter resolves per `RecvMsg`, so updates
  reach streams that are already open. The governed value is the constructed
  `Limiter`, not raw numbers — parsing and construction happen once per
  update, and the request path only reads a pointer.
- **Timeout** (`middleware/timeout`): `WithRules(*governance.Rules[time.Duration])`
  resolves the deadline per request; `ParseRule` reads a duration string and
  rejects non-positive values. The middleware only shortens deadlines — an
  earlier deadline inherited from the context wins and is not remapped to
  `ErrTimeout`. The transport servers' connection-level timeouts are
  unchanged; this middleware is the method-level, hot-updatable layer.

## Alternatives Rejected

- **A governance manager that owns middleware.** A central registry mapping
  config sections to middleware instances would add a second composition
  system next to the existing chain and generated plans. Rejected: the rule
  table composes with what exists; ownership stays with the application.
- **Rebuilding middleware chains on update.** Recomposing and re-registering
  handlers on each config change would make updates transactional but races
  registration, defeats registration-time composition (see
  [`generated-middleware.md`](generated-middleware.md)), and turns a
  parameter change into a structural change. Rejected in favor of parameters
  that are cheap atomic reads inside stable chains.
- **Pattern matching on operations** — prefixes, globs, regexes on rule keys.
  Rejected: `Operation` is opaque by contract, and any pattern language
  invites parsing it. Exact match plus one literal wildcard key is the whole
  matching model. Applications needing structural scoping already have the
  selector middleware and generated per-method plans, which operate at
  composition time on names the application owns.
- **Per-rule error tolerance** — installing the valid subset of a partially
  invalid snapshot. Rejected: see Failure Semantics.
- **Generic config-struct watching** (`Watch[T]` scanning the section into
  one struct without per-operation keys). Rejected as the primary shape: it
  pushes operation matching into every middleware by hand. A middleware that
  wants one global governed value can still use a table with only a `*` rule.
- **A new dependency or config schema registry.** No new module requirement
  and no schema language: rules are ordinary config sections decoded by the
  existing readers.
