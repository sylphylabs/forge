# Config Lifecycle

Status: implemented

Last reviewed: August 12, 2026

## Purpose

This document defines the ownership and lifecycle contract for the config
coordinator (`config.Config`) and its providers (`config.Source`,
`config.Watcher`). It is the design authority for who owns which goroutine,
what a caller may observe during construction and shutdown, and what a
provider must and must not do. The usage contract is in
`docs/agent/application.md`.

The contract exists because the inherited coordinator had lifecycle defects,
not provider-specific policy problems: it started each watch goroutine
immediately after its watcher was created, so a later `Source.Watch` failure
left earlier watchers running; `Close` returned after the first stop error,
did not join watch loops, and could not interrupt retry sleeps; and the
provider interfaces took no contexts, so nothing could interrupt a provider
stuck in I/O.

## Invariants

- The coordinator owns every watcher and goroutine it creates.
- Construction publishes one resolved snapshot only after all required
  startup work succeeds; no half-initialized `Config` is ever observable.
- Partial watcher construction rolls back every constructed watcher in
  reverse order and preserves all independent errors with `errors.Join`.
- `Close` is concurrent-safe and idempotent. Every caller observes the same
  terminal error.
- Shutdown attempts every watcher stop and joins every coordinator goroutine.
- Coordinator cancellation interrupts retry waiting immediately.
- A watcher notification never publishes a partially loaded or unresolved
  snapshot.
- Providers do not own coordinator retry, logging, or process-lifetime
  policy.

## The contract

Construction is loading. There is no separate `Load` step and therefore no
"constructed but unloaded" state to misuse:

```go
func New(ctx context.Context, opts ...Option) (*Config, error)

type Source interface {
    Load(ctx context.Context) ([]*KeyValue, error)
    Watch(ctx context.Context) (Watcher, error)
}

type Watcher interface {
    Next(ctx context.Context) ([]*KeyValue, error)
    Stop() error
}
```

`New` performs these steps:

1. Load every source and build the complete resolved reader without
   publishing it.
2. Construct every source watcher without starting a watch loop.
3. If construction fails, stop constructed watchers in reverse order, join
   rollback errors with the initiating error, and return the joined error.
4. Publish the reader, then start one owned watch loop per watcher. The
   loops detach from the construction context (`context.WithoutCancel`) and
   run until `Close`, because they serve the Config's lifetime, not the
   constructor call.

The context passed to `New` bounds construction only: initial loads and
watcher setup. Providers must honor its cancellation, which is what makes a
provider stuck in initial I/O interruptible.

`Close` performs these steps once; later calls return the stored result:

1. Cancel the watch-loop context, which interrupts blocked `Next` calls and
   retry waits.
2. Call `Stop` for every constructed watcher in reverse order, even after an
   earlier stop error.
3. Wait for every watch loop to return.
4. Join all stop errors and store the stable result.

Each watch loop checks coordinator cancellation before and after `Next`. A
non-terminal watcher error waits on a cancelable timer — never an
uninterruptible sleep — before retrying, so a persistently failing source
cannot spin the loop and cannot delay shutdown. A watcher may return
`(nil, nil)` when it cannot enumerate what changed; the coordinator reloads
every source after any notification, because cross-source placeholder
references make a full rebuild necessary either way.

## Provider requirements

- `Load` and `Watch` must honor context cancellation; both may block on I/O.
- `Watcher.Stop` must unblock an in-flight `Next` promptly and be safe to
  call more than once.
- A provider must not recreate watches with `context.Background` after its
  owner has canceled them, must not log through process globals, and must
  not start goroutines the coordinator cannot join.

Retry pacing, error logging, and reload policy live in the coordinator, so
every provider gets identical lifecycle behavior and a provider defect
cannot change process-wide policy.

## Acceptance tests

The load-bearing behavior is pinned by tests in `config`
(`config/config_test.go`):

- When a later source fails to create a watcher, earlier watchers are
  stopped and no watch loop is left consuming their signals
  (`TestNewRollsBackWatchersOnFailure`).
- A change in one source republishes values that textually live in another,
  because reload rebuilds the full snapshot
  (`TestConfigWatchReloadsCrossSourceReferences`).
- Observer registration validates its key and observer up front
  (`TestConfigWatchMissingKey`, `TestConfigWatchNilObserverPanics`), and
  multiple observers of one key all fire
  (`TestConfigWatchMultipleObservers`).
- `go test -race ./config ./config/file ./config/env` and `go vet` pass for
  those packages.

Nested provider modules under `contrib/config/` are separate modules with
their own gates; focused root-module tests do not prove a provider's
conformance. In particular, several contrib providers still construct their
own `context.Background` watch contexts — bringing each of them under the
caller-owned-context rule is per-provider work, tracked per module.
