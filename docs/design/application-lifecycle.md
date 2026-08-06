# Application Lifecycle

Status: Phase 0 failure-path hardening implemented in the current worktree;
concurrent startup admission and Phase 1 public API proposed

Last reviewed: July 28, 2026

## Purpose

This document narrows the application-lifecycle workstream from
[`runtime-modernization.md`](runtime-modernization.md) into reviewable phases.
It separates compatibility-preserving failure cleanup from the later public API
that will make context ownership and readiness explicit.

The current `App.Run()` contract starts blocking transport loops, registers the
service after those goroutines have entered, installs process signal handlers,
and waits for shutdown. Goroutine entry is not a readiness proof. An error from
a startup hook, registration, or an after-start hook must not leave listeners,
registrations, or transport goroutines behind.

## Current Worktree Status

The compatibility-preserving implementation now:

- invokes transport cleanup and shutdown hooks when endpoint construction,
  `BeforeStart`, `Register`, transport startup, or `AfterStart` fails;
- tracks successful registration and never deregisters a failed or skipped
  registration;
- compensates a successful `Register` that returns after a concurrent `Stop`,
  even when the registrar ignored context cancellation;
- waits for transport run and stop goroutines and joins independent lifecycle
  errors;
- suppresses bare `context.Canceled` only after parent cancellation or requested
  shutdown, while retaining cancellation returned as an unexpected startup
  failure; and
- keeps metadata and endpoint snapshots under application ownership and returns
  copies from the `AppInfo` getters.

The current implementation does not yet claim the complete target lifecycle.
It has no start-admission state machine, so cancellation before or during
endpoint construction and startup hooks still belongs to Phase 1. Transport
stops currently begin concurrently rather than in reverse declaration order,
and joining a third-party `Start` remains cooperative. HTTP and gRPC listeners
opened by `Endpoint` before `Start` need a transport-owned pre-start cleanup
contract in Phase 2.

## Invariants

Every phase must preserve these invariants:

- A component that opens a listener or starts a goroutine owns its cleanup and
  can be joined.
- A startup failure rolls back every completed stage in reverse order.
- Deregistration happens only after registration succeeded.
- Readiness becomes false before deregistration and transport draining.
- Shutdown is idempotent and bounded, and it preserves independent errors with
  `errors.Join`.
- Hooks never receive an accidentally canceled context.
- Process signal handling is host policy, not an intrinsic property of an
  embeddable application.
- No application lifecycle path mutates process-global logging, telemetry,
  codec, or selector state.

## Phase 0: Compatibility-Preserving Correctness

Phase 0 keeps the existing public `App`, `Option`, and `transport.Server`
signatures. It closes concrete failure-path leaks before the public lifecycle
API changes.

Required behavior:

1. Track whether service registration completed. `Stop` must not deregister an
   instance whose `Register` call failed or never ran.
2. If `BeforeStart`, server startup, registration, or `AfterStart` fails, cancel
   the application and stop every transport that may own a listener or
   goroutine.
3. Wait for all started transport loops and stop operations before `Run`
   returns.
4. Run bounded shutdown hooks on failure as well as normal shutdown, while
   preserving the original startup error.
5. Join startup, hook, registration, deregistration, transport-stop, and
   after-stop errors instead of returning the first one.

Phase 0 does not claim that a transport is ready merely because `Start` began.
It retains the inherited registration barrier until Phase 1 introduces an
explicit readiness contract.

Acceptance tests:

- `BeforeStart` failure after endpoint construction invokes `Stop` for every
  transport. Standard-transport listener closure is proven in Phase 2 because
  the inherited interface has no pre-start resource-release contract.
- Registration failure stops and joins transports without deregistering.
- `AfterStart` failure deregisters, stops, and joins transports.
- A transport startup failure prevents or rolls back registration.
- A registration that succeeds after concurrent shutdown is compensated
  exactly once.
- Concurrent and repeated `Stop` calls execute each shutdown stage once.
- Cleanup errors remain discoverable with `errors.Is` alongside the initiating
  startup error.
- Unexpected transport cancellation remains an error, while cancellation caused
  by requested shutdown is suppressed.
- Focused tests pass under the race detector without sleeps.

## Phase 1: Context-First Application Control

Phase 1 adds the new embedding path while retaining a documented migration path
for `Run()`:

```go
func (a *App) RunContext(ctx context.Context) error
```

`RunContext` uses the caller's context as the primary lifetime. The existing
`Run()` remains temporarily as a host-oriented compatibility wrapper and is
deprecated before v1. A small command helper may use `signal.NotifyContext` and
then call `RunContext`; the core application does not install signal handlers.

The application exposes an explicit state model:

```text
new -> starting -> ready -> stopping -> stopped
          |          |
          +-> failed <-+
```

Invalid repeated starts fail explicitly. `Stop` remains safe before, during,
and after `RunContext` and is idempotent.

## Phase 2: Transport Readiness

The standard HTTP, gRPC, and message transports must expose readiness without
changing the blocking run-loop meaning of `Start`. The focused transport
proposal must freeze the exact interface, but it must support:

- readiness success only after the listener or subscription can accept work;
- readiness failure when the run loop exits during startup;
- cancellation while readiness is pending;
- readiness becoming false before drain begins;
- one deterministic terminal error observable by both readiness and join paths.

During migration, third-party implementations of the existing
`transport.Server` interface may use the legacy start-entry barrier. Standard
Forge transports must use explicit readiness, and the fallback must be
observable so it cannot be mistaken for a strong guarantee.

Service registration begins only after every required transport reports ready.
If any transport fails, ready transports roll back in reverse declaration order.

## Shutdown Order

The target shutdown sequence is:

1. Atomically leave the ready state and reject new lifecycle admission.
2. Run `BeforeStop` hooks with one bounded shutdown context.
3. Deregister a successfully registered service instance.
4. Stop transports in reverse declaration order.
5. Join every transport run loop and owned background goroutine.
6. Run `AfterStop` hooks with a fresh bounded context that preserves application
   values.
7. Publish the stopped state and the stable joined terminal error.

Calling `Stop` initiates this sequence. `RunContext` remains the join boundary
and returns the complete terminal error. A later API may add an explicit
`Wait`, but Phase 1 must not create two competing owners for the same result.

## Error Semantics

Lifecycle errors are operational errors, not public RPC status values. Each
stage adds concise context while preserving the original error for `errors.Is`
and `errors.As`. Cancellation is suppressed only when it is the expected result
of an otherwise successful requested shutdown; independent cleanup errors are
never suppressed.

## Migration and Validation

Each public phase updates `COMPATIBILITY.md`, `COMPATIBILITY_zh.md`, the two
Kratos migration guides, and runnable examples together. Validation includes:

- focused unit tests and `go test -race` for the root package;
- HTTP, gRPC, and message transport lifecycle tests;
- leak checks for every startup and shutdown stage;
- an embedding test with two independently canceled applications;
- `go vet`, the complete module-aware test command, and an external consumer
  once a public API phase lands.

Logger, codec, selector, telemetry, config-watcher, and HTTP request-budget work
remain separate workstreams. They may share lifecycle primitives after those
primitives land, but they must not be bundled into the application cutover.
