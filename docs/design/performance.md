# Performance Modernization

Status: active design and validation

Last verified: July 22, 2026

## Purpose

This document records OpenKratos performance investigations, adoption decisions,
acceptance criteria, and follow-up work. Reported upstream benchmark results are
inputs to our investigation, not OpenKratos release claims. A result becomes an
OpenKratos result only after it is reproduced against an OpenKratos baseline on
the same machine and Go toolchain.

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
| HTTP routing | In progress | Replace Gorilla mux with `net/http.ServeMux` plus a small Google AIP template adapter. |
| WRR selection | Approved for adoption | Port go-kratos/kratos#3831 with upstream authorship and reproduce its benchmarks. |
| P2C selection | Approved for adoption | Port go-kratos/kratos#3832 with upstream authorship and reproduce its concurrency benchmarks. |
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

## WRR Hot Path

### Source

- Upstream PR: [go-kratos/kratos#3831](https://github.com/go-kratos/kratos/pull/3831)
- Upstream commit: `15639816e6a0d0f7f05bbcc865bd0f70e996e15e`
- Upstream status when verified: open

### Finding

WRR is the default selector for both HTTP and gRPC clients. Its `Pick` method is
called before every discovered outbound RPC. The current implementation builds
an address set on steady-state picks to determine whether nodes changed.

The proposed implementation relies on this invariant:

```text
after selection: currentWeight keys = previous node set union current node set
```

Node addresses are contractually unique within a service. Therefore,
`len(currentWeight) > len(nodes)` exactly identifies stale entries after the
current nodes have been visited. Cleanup remains O(n), but runs only when a node
has disappeared. WRR selection itself remains O(n).

### Upstream-reported result

The upstream PR reports the following Apple M1 Pro, Go 1.26 measurements. These
have not yet been reproduced as OpenKratos results.

| Benchmark | Before | After | Reported change |
| --- | ---: | ---: | ---: |
| Stable nodes | 225.4 ns/op | 120.8 ns/op | -46.4% |
| Changing nodes | 232.7 ns/op | 209.2 ns/op | -10.1% |
| Changing-node allocation | 32 B, 1 alloc | 0 B, 0 alloc | allocation removed |

### Adoption requirements

- Preserve the upstream author and add the source SHA with `cherry-pick -x`.
- Adapt new imports to `github.com/openkratos/kratos`.
- Retain equal-length replacement, partial-overlap, and randomized churn tests.
- Benchmark stable, add-only, removal, and replacement workloads at 1, 5, 10,
  and 100 nodes.
- Add a parallel benchmark to measure the reduced mutex hold time.
- Run selector tests and race detection before acceptance.

## P2C Concurrency

### Source

- Upstream PR: [go-kratos/kratos#3832](https://github.com/go-kratos/kratos/pull/3832)
- Upstream commit: `584de99479d3c45ec61701a3d51a0ee84473d9c2`
- Upstream status when verified: open

### Finding

P2C samples two distinct nodes and uses the EWMA node state to choose the better
candidate. The current implementation serializes every concurrent sample on a
per-balancer mutex protecting a private `rand.Rand`.

The top-level `math/rand/v2` functions are documented as safe for concurrent
use. In the currently supported Go implementation they use runtime-local random
state, so P2C does not need its own mutex or random generator. Other shared P2C
and EWMA state already uses typed atomics.

This changes the random source from a time-seeded per-balancer stream to the Go
runtime source. P2C does not expose deterministic replay or seeding as a public
contract, so this is not considered an API compatibility break.

### Upstream-reported result

The upstream PR reports the following Apple M1 Pro, Go 1.26,
`GOMAXPROCS=8` measurements. These have not yet been reproduced as OpenKratos
results.

| Benchmark | Before | After | Reported change |
| --- | ---: | ---: | ---: |
| Parallel selection | 329.5 ns/op | 128.1 ns/op | -61.1% |
| Serial selection | 274.3 ns/op | 273.4 ns/op | no significant change |

P2C is optional; WRR remains the current default. This improvement therefore
benefits applications that explicitly select P2C and does not stack with the WRR
result for a single request.

### Adoption requirements

- Preserve the upstream author and add the source SHA with `cherry-pick -x`.
- Adapt new imports to `github.com/openkratos/kratos`.
- Avoid comments that make lock freedom a permanent standard-library API
  guarantee; concurrency safety is the contractual requirement.
- Run the selector suite with race detection.
- Reproduce serial and parallel results with `GOMAXPROCS` 1, 2, 4, 8, and 16.
- Verify that sampled nodes are always distinct and that distribution remains
  within a documented statistical tolerance.

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

- Complete and validate the `ServeMux` migration.
- Establish a clean OpenKratos baseline commit before importing upstream work.
- Cherry-pick the WRR and P2C commits separately, preserving authorship and
  upstream SHAs.
- Adapt module paths and keep OpenKratos-specific benchmark improvements in
  follow-up commits.

### Before the first release

- Publish controlled Gorilla-versus-ServeMux benchmark results.
- Publish controlled before-versus-after WRR and P2C results.
- Add routing and selector performance commands to the development guide.
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
