# Config Lifecycle

Status: proposed implementation contract

Last reviewed: July 28, 2026

## Purpose

This document narrows the config portion of
[`runtime-modernization.md`](runtime-modernization.md) into two independently
reviewable phases. Phase 0 fixes coordinator ownership without changing the
existing `Config`, `Source`, or `Watcher` method signatures. Phase 1 introduces
context-aware provider APIs after a migration contract is approved.

The current coordinator starts each watch goroutine immediately after its
watcher is created. A later `Source.Watch` failure therefore leaves earlier
watchers running. `Close` returns after the first stop error, does not join watch
loops, and cannot interrupt the fixed retry sleep. These are lifecycle defects,
not provider-specific policy.

## Invariants

- The coordinator owns every watcher and goroutine it creates.
- `Load` publishes one resolved snapshot only after all required startup work
  succeeds.
- Partial watcher construction rolls back every constructed watcher in reverse
  order and preserves all independent errors with `errors.Join`.
- `Close` is concurrent-safe and idempotent. Every caller observes the same
  terminal error.
- Shutdown attempts every watcher stop and joins every coordinator goroutine.
- Coordinator cancellation interrupts retry waiting immediately.
- A watcher notification never publishes a partially loaded or unresolved
  snapshot.
- Providers do not own coordinator retry, logging, or process-lifetime policy.

## Phase 0: Coordinator Ownership

Phase 0 retains these public signatures:

```go
type Config interface {
    Load() error
    Scan(any) error
    Value(string) Value
    Watch(string, Observer) error
    Close() error
}

type Source interface {
    Load() ([]*KeyValue, error)
    Watch() (Watcher, error)
}

type Watcher interface {
    Next() ([]*KeyValue, error)
    Stop() error
}
```

The implementation adds a private lifecycle with `new`, `loading`, `loaded`,
`closing`, and `closed` states, an owned cancellation function, a join group,
and one stable close result.

`Load` performs these steps:

1. Reserve the `new -> loading` transition. Concurrent or repeated successful
   loads return `ErrAlreadyLoaded`; loading after close returns `ErrClosed`.
2. Build the complete resolved reader without publishing it.
3. Construct every source watcher without starting a watch loop.
4. If construction fails, stop constructed watchers in reverse order, join
   rollback errors with the initiating error, and return to `new` so callers may
   retry.
5. Commit the reader and watcher set atomically, transition to `loaded`, then
   start the owned watch loops.

`Close` performs these steps once:

1. Transition to `closing` and cancel coordinator retry waits.
2. Call `Stop` for every constructed watcher in reverse order, even after an
   earlier stop error.
3. Wait for every watch loop to return.
4. Join all stop errors, store the stable result, and transition to `closed`.

Each watch loop checks coordinator cancellation before and after `Next`. A
legacy watcher may return `(nil, nil)` when `Stop` unblocks it; after cancellation
that result exits without reload. Non-terminal errors use a cancelable timer
instead of `time.Sleep`. Phase 0 retains the current retry duration and does not
yet add jitter or provider error classification.

Because the legacy interfaces have no context, Phase 0 cannot interrupt a
provider stuck inside initial `Load` or `Watch`. The `Watcher` contract is
clarified: `Stop` must cause an in-flight `Next` to return promptly.

## Phase 1: Context-Aware Providers

Phase 1 replaces implicit lifetime with caller-owned contexts. The focused API
proposal must decide whether context is added to the existing methods or exposed
through new interfaces and adapters. It must cover:

- cancellation during initial source load and watcher construction;
- centralized exponential backoff, jitter, and terminal-error classification;
- immutable event snapshots and an explicit stale-data policy;
- observer ordering, reentry, panic, and slow-consumer behavior; and
- provider conformance tests for file and supported remote providers.

Remote providers must not recreate watches with `context.Background` after
their owner has canceled them. Phase 1 includes a provider-by-provider migration
ledger; changing only the root coordinator is not sufficient evidence.

## Compatibility

Phase 0 adds `ErrAlreadyLoaded` and `ErrClosed` as diagnosable sentinel errors.
This tightens previously unspecified repeated-load behavior without changing
method signatures. `Close` before `Load` remains valid and transitions directly
to `closed`. A failed first `Load` remains retryable after rollback.

The new behavior must be recorded in the compatibility and migration documents
when implementation lands. Until then this document is a proposal, not current
runtime behavior.

## Acceptance Tests

- When the second source fails to create a watcher, the first watcher is stopped
  and no watch loop was started. Startup and rollback errors remain discoverable
  with `errors.Is`.
- Every watcher is stopped even when more than one `Stop` returns an error, and
  the returned error contains all failures.
- `Close` waits until a blocked `Next` and its watch loop have returned.
- Coordinator cancellation interrupts retry waiting without a test sleep.
- A watcher returning `(nil, nil)` after stop does not reload or loop again.
- Concurrent and repeated `Close` calls stop each watcher once and return the
  same result.
- Repeated `Load`, `Close` before `Load`, failed-load retry, and `Load` after
  close follow the documented state transitions.
- `go test -race ./config ./config/file ./config/env`, `go vet` for the same
  packages, and `git diff --check` pass.

Nested provider modules and complete repository validation remain separate
gates. Focused root-module tests do not prove every provider migration.
