# Performance Modernization

Status: active design and validation

Last verified: July 22, 2026

## Purpose

This document records OpenKratos performance investigations, adoption decisions,
acceptance criteria, and follow-up work. Reported upstream benchmark results are
inputs to our investigation, not OpenKratos release claims. A result becomes an
OpenKratos result only after it is reproduced against an OpenKratos baseline on
the same machine and Go toolchain.

Individual upstream review outcomes and provenance are tracked in
[`docs/upstream-adoptions.md`](../upstream-adoptions.md).

## Goals

- Reduce CPU, allocation, garbage-collection, and lock costs on per-request hot
  paths.
- Prefer current Go standard-library implementations when they meet the required
  behavior and have measurable operational benefits.
- Keep correctness and observable routing or balancing behavior ahead of a
  benchmark result.
- Preserve upstream authorship and source commit provenance for adopted work.
- Document intentional compatibility breaks instead of hiding them behind a
  slow compatibility layer.

## Non-goals

- A microbenchmark improvement is not presented as the same improvement in
  end-to-end RPC latency.
- WRR and P2C improvements are not added together. A request normally uses one
  balancing strategy, not both.
- OpenKratos does not adopt an unreleased Go API only because it appears on a
  milestone. The final API and toolchain baseline must be reviewed first.

## Current Decisions

| Area | Status | Decision |
| --- | --- | --- |
| HTTP routing | Implemented; release evidence pending | Use `net/http.ServeMux` plus a small Google AIP template adapter. |
| WRR selection | Adopted and validated | Detect stale entries without a steady-state node-set scan; retain churn invariants and benchmarks. |
| P2C selection | Adopted and validated | Use concurrency-safe `math/rand/v2` package functions instead of a per-balancer random-number lock. |
| Codec content subtype | Implemented and validated | Fast-path exact JSON and scan remaining delimiters with `IndexByte`. |
| Go 1.27 HTTP/2 | Validated | Use the standard-library-backed HTTP/2 path through a current `golang.org/x/net`; do not retain the legacy build tag. |
| Future Go APIs | Watch | Evaluate only accepted, shipped APIs against concrete Kratos call sites and benchmarks. |

## HTTP Routing

### Finding

Upstream issue [go-kratos/kratos#3820](https://github.com/go-kratos/kratos/issues/3820)
identified Gorilla mux routing as a candidate request-path cost. Go's
`net/http.ServeMux` provides method patterns, path wildcards, precedence rules,
and `Request.PathValue`. OpenKratos can use it for the routing tree while keeping
Google AIP template expansion in a small internal adapter.

### Compatibility boundary

- Google AIP variables, terminal `**`, custom verbs, and common single-segment
  constraints remain supported.
- Arbitrary multi-segment regular expressions and non-terminal `**` are rejected.
- Standard-library precedence and conflict detection replace Gorilla
  registration-order behavior.
- Prefix handlers use path-segment subtree semantics.

These are intentional pre-v1 changes. The detailed developer-facing behavior is
recorded in `DEVELOPMENT.md`.

### Acceptance gate

- All generated AIP route forms pass focused routing and binding tests.
- Custom 404 and 405 handlers, route walking, middleware metadata, prefixes, and
  headers retain their documented behavior.
- Gorilla and standard-library baselines are compared at 1, 100, and 1,000
  routes, including static routes, variables, misses, and parallel dispatch.
- Results report `ns/op`, `B/op`, and `allocs/op` using at least ten samples and
  `benchstat`.
- Routing compatibility breaks have migration examples before release.

The implementation and focused benchmarks are present in
`transport/http/routing.go` and `transport/http/routing_benchmark_test.go`.
The remaining gate is controlled, publishable before-and-after evidence using
the benchmark rules below.

## WRR Hot Path

### Source

- Upstream PR: [go-kratos/kratos#3831](https://github.com/go-kratos/kratos/pull/3831)
- Upstream commit: `15639816e6a0d0f7f05bbcc865bd0f70e996e15e`
- OpenKratos adoption commit: `42b647be57a1cff372346c42aceb0bfba2e1e99c`
- OpenKratos acceptance-test commit: `4fa25d1bc3d68a45d0da97e01c82c8785b93d01d`
- Upstream status when verified: open

### Finding

WRR is the default selector for both HTTP and gRPC clients. Its `Pick` method is
called before every discovered outbound RPC. The upstream baseline built an
address set on steady-state picks to determine whether nodes changed.

The proposed implementation relies on this invariant:

```text
after selection: currentWeight keys = previous node set union current node set
```

Node addresses are contractually unique within a service. Therefore,
`len(currentWeight) > len(nodes)` exactly identifies stale entries after the
current nodes have been visited. Cleanup remains O(n), but runs only when a node
has disappeared. WRR selection itself remains O(n).

### Upstream-reported result

The upstream PR reports the following Apple M1 Pro, Go 1.26 measurements. They
remain upstream evidence; the controlled OpenKratos results are recorded below.

| Benchmark | Before | After | Reported change |
| --- | ---: | ---: | ---: |
| Stable nodes | 225.4 ns/op | 120.8 ns/op | -46.4% |
| Changing nodes | 232.7 ns/op | 209.2 ns/op | -10.1% |
| Changing-node allocation | 32 B, 1 alloc | 0 B, 0 alloc | allocation removed |

### OpenKratos result

Ten-sample Apple M5 Pro measurements on Go 1.26.4 show stable picks improving
40.1% to 49.6% from 5 to 100 nodes and shared parallel picks improving 45.5%.
A synthetic five-node full-replacement cycle regressed 24.6%; replacement at 10
and 100 nodes showed no significant change. The full commands, allocation data,
and accepted trade-off are in the
[selector benchmark report](../benchmarks/selectors-2026-07-22.md).

### Adoption record

- Upstream authorship and the source SHA are preserved in the local commit.
- Imports use the OpenKratos module path.
- Equal-length replacement, partial-overlap, and randomized churn invariants are
  retained.
- Stable, add-only, removal, replacement, and parallel benchmarks are present at
  the required node counts.
- Controlled before-and-after `benchstat` results are published, and the selector
  suite passes race detection.

## P2C Concurrency

### Source

- Upstream PR: [go-kratos/kratos#3832](https://github.com/go-kratos/kratos/pull/3832)
- Upstream commit: `584de99479d3c45ec61701a3d51a0ee84473d9c2`
- OpenKratos adoption commit: `f1246466a2df872e7a4fa05b7e18a212e064fc63`
- OpenKratos acceptance-test commit: `4fa25d1bc3d68a45d0da97e01c82c8785b93d01d`
- Upstream status when verified: open

### Finding

P2C samples two distinct nodes and uses the EWMA node state to choose the better
candidate. The upstream baseline serialized every concurrent sample on a
per-balancer mutex protecting a private `rand.Rand`; the adopted implementation
removes that lock and private generator.

The top-level `math/rand/v2` functions are documented as safe for concurrent
use. In the currently supported Go implementation they use runtime-local random
state, so P2C does not need its own mutex or random generator. Other shared P2C
and EWMA state already uses typed atomics.

This changes the random source from a time-seeded per-balancer stream to the Go
runtime source. P2C does not expose deterministic replay or seeding as a public
contract, so this is not considered an API compatibility break.

### Upstream-reported result

The upstream PR reports the following Apple M1 Pro, Go 1.26,
`GOMAXPROCS=8` measurements. They remain upstream evidence; the controlled
OpenKratos results are recorded below.

| Benchmark | Before | After | Reported change |
| --- | ---: | ---: | ---: |
| Parallel selection | 329.5 ns/op | 128.1 ns/op | -61.1% |
| Serial selection | 274.3 ns/op | 273.4 ns/op | no significant change |

### OpenKratos result

Ten-sample Apple M5 Pro measurements on Go 1.26.4 show parallel improvements of
17.6%, 38.5%, 54.8%, and 68.6% at GOMAXPROCS 2, 4, 8, and 16 respectively.
GOMAXPROCS 1 regressed 0.9%, serial results stayed within -1.1% to +3.7%, and
allocations were unchanged. See the
[selector benchmark report](../benchmarks/selectors-2026-07-22.md).

P2C is optional; WRR remains the current default. This improvement therefore
benefits applications that explicitly select P2C and does not stack with the WRR
result for a single request.

### Adoption record

- Upstream authorship and the source SHA are preserved in the local commit.
- Imports use the OpenKratos module path.
- Comments rely on the standard library's concurrency-safety contract rather
  than promising an implementation detail.
- Concurrent sampling, distinct-node selection, and distribution tolerance have
  regression coverage.
- Serial and parallel results across `GOMAXPROCS` 1, 2, 4, 8, and 16 are
  published, and the selector suite passes race detection.

## Content Subtype Hot Path

### Source and scope

- Upstream PR: [go-kratos/kratos#3659](https://github.com/go-kratos/kratos/pull/3659)
- Upstream commit: `ebe5cf9a26749fc2467e3cf1584b539f0112dcbe`
- OpenKratos baseline: `4c3d353eac847f9647990b945477add6aca0dec1`
- OpenKratos implementation: `5a4fea3c01f5853e82be5edda6b41e5e98f0b88c`

`ContentSubtype` runs while selecting request and response codecs. The adopted
change adds an exact `application/json` fast path and uses `strings.IndexByte`
for the remaining delimiter scans. This is a function-level microbenchmark; it
is not an end-to-end HTTP or RPC latency claim.

### OpenKratos result

Ten samples were collected on Apple M5 Pro, macOS 27.0, Go 1.26.4,
`darwin/arm64`, and `GOMAXPROCS=1`. Baseline and current functions were compiled
as equivalent benchmark packages and compared with `benchstat`.

The persistent benchmark from the worktree can be applied unchanged to a
detached baseline checkout and reproduced with:

```shell
GOMAXPROCS=1 go test ./internal/httputil -run '^$' \
  -bench '^BenchmarkContentSubtype$' -benchmem -count=10 > old.txt
GOMAXPROCS=1 go test ./internal/httputil -run '^$' \
  -bench '^BenchmarkContentSubtype$' -benchmem -count=10 > new.txt
benchstat old.txt new.txt
```

| Input | Baseline | Current | Change |
| --- | ---: | ---: | ---: |
| `application/json` | 6.890 ns/op | 2.502 ns/op | -63.69% |
| `application/json; charset=utf-8` | 6.108 ns/op | 4.546 ns/op | -25.58% |
| `text/html; charset=utf-8` | 6.007 ns/op | 4.899 ns/op | -18.45% |

All comparisons report `p=0.000`, `n=10`. Both implementations remain at
`0 B/op` and `0 allocs/op`. The persistent benchmark is
`internal/httputil.BenchmarkContentSubtype`.

## Benchmark Rules

Performance changes must include enough information to reproduce the claim:

- Exact before and after commits.
- Go version, operating system, architecture, CPU, and `GOMAXPROCS`.
- Benchmark command, sample count, and `benchstat` output.
- `ns/op`, `B/op`, and `allocs/op`; throughput where it is meaningful.
- Separate serial and parallel measurements for shared client state.
- Correctness and race tests run independently of benchmarks.

Fixed nanosecond thresholds should not be used as CI gates because shared CI
hardware is noisy. CI should verify correctness and benchmark compilation;
release evidence should come from controlled comparative runs.

## Work Plan

### Immediate

- Complete the controlled Gorilla-versus-ServeMux comparison.
- Investigate replacement-heavy WRR workloads if real discovery profiles show
  that the isolated five-node regression is operationally relevant.
- Keep performance changes separate from unrelated upstream adoptions.

### Before the first release

- Publish controlled Gorilla-versus-ServeMux benchmark results.
- Document selector choice and the operational difference between WRR and P2C.
- Review dependency removal made possible by the standard-library router.

### Ongoing

- Profile real RPC workloads before optimizing additional selector, codec,
  middleware, or transport paths.
- Track accepted Go release changes, including standard UUID support and future
  generic collection or structured metadata APIs, but evaluate them only after
  their final API and release target are known.
- Prefer removal of dependencies and compatibility layers only when behavior,
  migration cost, and measured benefit are documented.
