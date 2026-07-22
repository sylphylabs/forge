# Upstream Adoption Ledger

Status: active

Last reviewed: July 22, 2026

## Purpose

OpenKratos preserves the go-kratos history but does not merge upstream changes
mechanically. This ledger records why a notable upstream proposal was adopted,
superseded, deferred for redesign, or rejected. It complements the synchronization
policy in [`UPSTREAM.md`](../UPSTREAM.md); it does not replace commit provenance.
User-visible behavior belongs in [`COMPATIBILITY.md`](../COMPATIBILITY.md) after
the implementation and validation are complete.

## Status Vocabulary

- **Adopted**: accepted into OpenKratos with a local commit and validation.
- **Implemented**: implemented and validated in the current worktree; the local
  commit and final provenance record are still pending.
- **Superseded**: the problem is handled by a different OpenKratos design.
- **Candidate**: the change is technically worthwhile but is not scheduled.
- **Planned**: accepted in principle and awaiting an isolated implementation.
- **Rework required**: the problem is valid, but the proposed patch is not safe
  or complete enough to import.
- **Rejected**: the proposed behavior conflicts with OpenKratos goals.

## Current Ledger

All upstream states in this table were verified on July 22, 2026.

| Upstream revision | Area | Decision | OpenKratos record | Rationale |
| --- | --- | --- | --- | --- |
| [PR #3831](https://github.com/go-kratos/kratos/pull/3831) @ `15639816` | WRR steady-state cost | Adopted | `42b647be`; tests `4fa25d1b`; [benchmarks](benchmarks/selectors-2026-07-22.md) | Removes the per-pick node-set scan while retaining churn invariants. |
| [PR #3832](https://github.com/go-kratos/kratos/pull/3832) @ `584de994` | P2C contention | Adopted | `f1246466`; tests `4fa25d1b`; [benchmarks](benchmarks/selectors-2026-07-22.md) | Removes the per-balancer random-number mutex and adds concurrency coverage. |
| [PR #3814](https://github.com/go-kratos/kratos/pull/3814) @ `af131a87` | Unsafe default HTTP fallback | Superseded | `transport/http/routing.go` | The ServeMux migration defaults to safe 404 and 405 handling without falling through to `http.DefaultServeMux`. |
| [PR #3851](https://github.com/go-kratos/kratos/pull/3851) @ `a3bfa746` | gRPC selector initialization panic | Adopted | `15aff6e0` | Lazy fallback to the global selector is covered by a real picker test. |
| [PR #3856](https://github.com/go-kratos/kratos/pull/3856) @ `e4697dc1` | HTTP path escaping | Adopted | `ec58dbf5` | Reimplemented with AIP-template-aware escaping: single-segment variables cannot inject route slashes, while structural multi-segment slashes remain intact. |
| [PR #3838](https://github.com/go-kratos/kratos/pull/3838) @ `1a937058` | Eureka endpoint metadata aliasing | Adopted | `71bae081` | Metadata is cloned per endpoint and source metadata remains unchanged. |
| [PR #3836](https://github.com/go-kratos/kratos/pull/3836) @ `b444c589` | EWMA cancellation health signal | Adopted | `0afec3e2` | Corrects `errors.Is` ordering and does not penalize caller cancellation unless a custom classifier explicitly requests it. |
| [PR #3852](https://github.com/go-kratos/kratos/pull/3852) @ `1c993d03` | `kratos run` relative paths | Superseded | Legacy CLI removed | OpenKratos uses the standard Go toolchain to run services and does not retain the `kratos run` wrapper. |
| [Issue #3854](https://github.com/go-kratos/kratos/issues/3854) | Project scaffold corrupts protobuf descriptors | Superseded | Legacy CLI removed | Blind module-path replacement mutates generated raw descriptors without updating their encoded lengths. OpenKratos removes the project-scaffolding CLI instead of preserving this implicit source rewrite. |
| [PR #3781](https://github.com/go-kratos/kratos/pull/3781) @ `bf65393e` | Eureka retry server selection | Adopted | `71bae081` | Defines `maxRetry` as retries after the first attempt, removes shared slice mutation, handles empty server lists, and replays request bodies. |
| [PR #3835](https://github.com/go-kratos/kratos/pull/3835) @ `e98b0686` | gRPC stream timeout | Rejected | None | Applying the default two-second unary timeout to every stream would terminate normal long-lived streams. Any stream timeout must be explicit and cover the full send/receive lifecycle. |
| [PR #3813](https://github.com/go-kratos/kratos/pull/3813) @ `04a41bad` | Opaque protobuf HTTP generation | Adopted | `a08e60ea` | Reimplemented with API-aware getters/setters and real Editions 2023 opaque generation-and-compilation coverage for message, scalar, repeated, and map fields. |
| [PR #3654](https://github.com/go-kratos/kratos/pull/3654) @ `c43974d3` | Empty HTTP request body | Adopted | `c3b8ecb5` | Empty bodies no longer require a registered Content-Type; non-empty bodies retain strict codec validation. |
| [PR #3562](https://github.com/go-kratos/kratos/pull/3562) @ `769dbc9a` | Consul transition to zero instances | Adopted | `133e56d9` | Watchers now receive an empty service list instead of retaining stale instances. |
| [PR #3658](https://github.com/go-kratos/kratos/pull/3658) @ `be57384e` | Consul endpoint port validation | Adopted | `133e56d9` | Missing, nonnumeric, zero, negative, and out-of-range ports are rejected. |
| [PR #3659](https://github.com/go-kratos/kratos/pull/3659) @ `ebe5cf9a` | `ContentSubtype` hot path | Adopted | `5a4fea3c`; [benchmark](design/performance.md#content-subtype-hot-path) | Adds the JSON fast path and `IndexByte`; ten-sample local results show 18.45% to 63.69% lower function cost with unchanged allocations. |
| [PR #3817](https://github.com/go-kratos/kratos/pull/3817) @ `8cf75ec2` | Canonical YAML v3 module | Adopted | `74ea666c` | Direct imports use `go.yaml.in/yaml/v3`; all affected nested module dependency graphs were synchronized without rewriting unrelated transitive imports. |
| [PR #3811](https://github.com/go-kratos/kratos/pull/3811) @ `e38d6c60` | Selector middleware assertions | Adopted | `511ad880` | Tests now prove client/server hit and miss behavior for prefix, regex, and path rules. |
| [PR #3812](https://github.com/go-kratos/kratos/pull/3812) @ `6eced1d6` | OTel stats-handler assertions | Adopted | `d0f0b460` | Stats tests assert emitted attributes instead of only exercising handlers. |
| [PR #3668](https://github.com/go-kratos/kratos/pull/3668) @ `947a694b` | gRPC full-method parsing | Adopted | `d0f0b460` | Invalid and extra-slash methods are rejected rather than silently normalized; original invalid values remain observable. |
| [PR #3416](https://github.com/go-kratos/kratos/pull/3416) @ `71d65bb0` | OTel semantic conventions | Adopted | `d0f0b460` | Upgrades to semconv v1.41 and keeps HTTP, gRPC, peer, service, and Kratos error attributes semantically distinct. |
| [PR #3228](https://github.com/go-kratos/kratos/pull/3228) @ `123f0543` / [PR #3111](https://github.com/go-kratos/kratos/pull/3111) @ `64865cf2` | App shutdown lifecycle | Adopted | `59817513` | Stop is idempotent, shutdown stages retain joined errors, and after-stop work receives a value-preserving bounded context. |
| [PR #2868](https://github.com/go-kratos/kratos/pull/2868) @ `2f376208` / [PR #3132](https://github.com/go-kratos/kratos/pull/3132) @ `2d386304` | Atomic config reload | Adopted | `2d0a7a5a` | Every watch event rebuilds and resolves a complete snapshot before atomic publication; hidden files are skipped internally. |
| [PR #3199](https://github.com/go-kratos/kratos/pull/3199) @ `4bba0c6a` / [PR #3478](https://github.com/go-kratos/kratos/pull/3478) @ `bd65ae3d` | etcd recovery and cancellation | Adopted | `fd27bead` | Registration recovery is bounded and context-aware; watcher close/cancel races no longer leak or spin. |
| [PR #3216](https://github.com/go-kratos/kratos/pull/3216) @ `e9564e1b` | HTTP endpoint path prefix | Adopted | `ec58dbf5` | Direct endpoints retain their base path while discovery service names remain separate. |
| [PR #3303](https://github.com/go-kratos/kratos/pull/3303) @ `74549b16` | Protobuf path parameter binding | Adopted | `ec58dbf5` | Field descriptors map proto text names to JSON query names, including nested fields and custom JSON names. |

## Adoption Requirements

Before an upstream proposal is marked **Adopted**:

1. Record the upstream PR and exact source commit.
2. Re-evaluate the change against the current OpenKratos architecture and Go
   baseline; do not assume a clean cherry-pick is correct.
3. Preserve upstream authorship and source provenance in the local commit.
4. Add focused correctness tests. Shared-state changes also require race tests.
5. Reproduce performance claims against a local before-and-after baseline under
   the rules in [`docs/design/performance.md`](design/performance.md).
6. Document public behavior changes and migration guidance before release.
7. Record the local commit in this ledger after validation completes.

## Entry Template

```markdown
## PR #NNNN: Short title

- Status: Planned
- Upstream PR: https://github.com/go-kratos/kratos/pull/NNNN
- Upstream commit: full SHA
- Local commit: pending
- Decision: concise technical reason
- Compatibility: public behavior and migration impact
- Validation: focused tests, race tests, benchmarks, and external dependencies
```
