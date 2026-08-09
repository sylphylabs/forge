# Transport Capability Interfaces: Readiness and Graceful Stop

Status: implemented

Last reviewed: August 9, 2026

## Purpose

This document defines the two optional transport capabilities that let a
lifecycle manager shut servers down without dropping in-flight work and let a
probe ask whether a server accepts new work: `transport.Healthzer` and
`transport.GracefulStopper`. It covers the interface semantics, how `App`
consumes them during shutdown, the readiness aggregation entry point, and the
alternatives that were rejected.

The application lifecycle these capabilities plug into is defined in
[`application-lifecycle.md`](application-lifecycle.md). Diagnostic probes are
a separate workstream; the aggregation entry point here is deliberately
minimal so a probe registry can consume it later without this document
predicting its API.

## Decision

`transport.Server` stays two methods. Capabilities are separate single-method
interfaces that consumers discover by type assertion, following the
`Endpointer` and `ReplyHeaderer` precedent:

```go
// transport
type Healthzer interface{ Healthz() bool }
type GracefulStopper interface{ GracefulStop(context.Context) error }
```

A wide server interface — `Serve`, `Stop`, `GracefulStop`, `Info`,
`Healthz` on one type, as in jupiter — forces every transport, including
third-party ones, to answer questions it may have no answer to. A message
transport has no meaningful drain distinct from close; a unidirectional
notifier has no readiness beyond its connection. Optional atomic interfaces
let each transport claim exactly the capabilities it has, and a `Server`
implementation that claims neither remains a complete citizen.

## Healthz Semantics

`Healthz` reports readiness, not liveness: whether the server can accept new
work right now. Liveness — is the process running at all — is expressed by
the process itself and needs no method; a hung-but-alive distinction is a
diagnostics concern, not a transport capability. Readiness is the signal load
balancers and orchestrators act on, and it is the one with a precise
lifecycle definition:

- false before the server accepts traffic;
- true while it accepts;
- false as soon as shutdown or draining begins — before the listener
  actually closes, so routing stops ahead of the drain.

`Healthz` must be safe to call concurrently with the lifecycle and must not
block.

The HTTP server backs `Healthz` with an atomic serving flag flipped by
`Start`, `GracefulStop`, and `Stop`. The gRPC server backs it with the
lifecycle-driven internal `grpc_health_v1` health service, so the answer a
sidecar gets over the wire and the answer `Healthz` gives in-process are the
same state. The internal health service reports `NOT_SERVING` until `Start`
resumes it; readiness before the listener serves would be a false claim. With
`CustomHealth` the registered health service is user-owned and may diverge
from `Healthz`, which keeps reporting the lifecycle state.

## GracefulStop and Stop

The two methods split by what happens to in-flight work when time runs out:

- `GracefulStop(ctx)` stops accepting new work and waits for in-flight work
  to finish. When ctx ends first it returns the context's error and leaves
  the drain running in the background; it never forces.
- `Stop(ctx)` terminates within the bounds the context allows, by force if
  necessary: the HTTP server force-closes connections when its `Shutdown`
  context expires, the gRPC server calls the underlying hard stop.

`Stop` on both built-in servers already drained first and forced only on
context expiry. That behavior is a contract users deploy against, so `Stop`
keeps it; `GracefulStop` is the same drain with the forcing removed and the
abandonment made visible as the context error. Re-splitting into
`Stop` = immediate and `GracefulStop` = drain was rejected: it would turn
every existing `Stop` call site into a connection-dropping stop.

On the gRPC server the two methods share one drain guarded by `sync.Once`:
concurrent or sequential `GracefulStop` and `Stop` calls observe the same
drain completion instead of racing two shutdown paths, and `Stop` after an
abandoned `GracefulStop` forces immediately rather than draining again.

## App Consumption

`App`'s per-server stop goroutine resolves the capability once:

```go
func stopServer(ctx context.Context, srv transport.Server) error
```

A server implementing `GracefulStopper` is drained first, bounded by the same
`StopTimeout` context every stop already receives — draining is what that
timeout has always meant on the built-in servers, so no third timeout concept
exists. When the drain returns nil the server is done and `Stop` is not
called. When the drain is abandoned or fails, `Stop` runs with the remaining
budget and forces termination. Abandonment by the stop context is the
designed fallback, not a fault, so only the forced `Stop`'s error is
reported then; an independent drain error stays joined to `Stop`'s result. A
server without the capability gets exactly the single `Stop(ctx)` call it
always got.

Server stops remain concurrent, not reverse-ordered. Reverse declaration
order is a stated target of the lifecycle workstream, but the current
errgroup structure gives each server an independent stop goroutine keyed on
context cancellation; sequencing them would serialize drain windows and
multiply worst-case shutdown time by the server count within one
`StopTimeout`. Ordered shutdown belongs to the lifecycle phase that
introduces the state machine, not to capability discovery.

## Readiness Aggregation

`App` aggregates readiness across its servers and itself satisfies
`transport.Healthzer`:

```go
func (a *App) Healthz() bool
```

The conjunction covers servers that implement `Healthzer`; a server without
the capability makes no claim and cannot veto. An application with no
reporting servers is vacuously ready, which matches the interface contract:
no claim is not a negative claim.

`transport/http/healthz` turns any `Healthzer` into a probe endpoint, shaped
like `transport/http/pprof`: a constructor returning an `http.Handler`,
nothing registered automatically.

```go
httpSrv.Handle("/healthz", healthz.NewHandler(app))
```

It answers `200 ok` while ready and `503 unavailable` otherwise, the
convention Kubernetes-style HTTP probes consume. Placing the handler package
under `transport/http` keeps the root `transport` package free of HTTP
dependencies.

## Rejected Alternatives

- **Widening `transport.Server`.** Breaks every existing implementation,
  including third-party servers in downstream modules, and forces
  capabilities onto transports that lack them.
- **`Healthz() error` or a status enum.** No consumer of a boolean readiness
  gate was found that could act on more structure; a diagnostics registry
  wanting rich status is a different interface with a different consumer,
  and inventing its shape here would prejudge that design.
- **A separate graceful-stop timeout option.** `StopTimeout` already bounds
  the drain on the built-in servers; a second knob would make two timeouts
  race for the same window.
- **`Stop` = immediate, `GracefulStop` = drain.** Re-splitting the existing
  drain-then-force `Stop` would silently drop in-flight work at every
  current call site.
- **Auto-registering `/healthz` on the HTTP server.** Route ownership
  belongs to the application; a framework-claimed path can collide with user
  routes and cannot choose the right `Healthzer` to serve.

## Validation

- Root package: shutdown prefers `GracefulStop`, falls back to `Stop` on
  abandonment or error, joins independent drain errors, and calls `Stop`
  exactly once on a server that implements only `Start`/`Stop`.
- `transport/http`, `transport/grpc`: readiness is false before start, true
  while serving, false once draining begins; `GracefulStop` completes
  in-flight work; an expired context abandons the wait while the drain
  continues.
- `transport/http/healthz`: handler status codes and bodies.
- All of the above under `go test -race`.
