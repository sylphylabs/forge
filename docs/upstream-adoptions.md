# Upstream Adoption Ledger

Status: active

Last reviewed: July 22, 2026

## Purpose

OpenKratos preserves the go-kratos history but does not merge upstream changes
mechanically. This ledger records why a notable upstream proposal was adopted,
superseded, deferred for redesign, or rejected. It complements the synchronization
policy in [`UPSTREAM.md`](../UPSTREAM.md); it does not replace commit provenance.

## Status Vocabulary

- **Adopted**: accepted into OpenKratos with a local commit and validation.
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
| [PR #3851](https://github.com/go-kratos/kratos/pull/3851) @ `a3bfa746` | gRPC selector initialization panic | Candidate | Not yet adopted | The nil dereference is reproducible and the fix is small; local integration and race tests are required. |
| [PR #3856](https://github.com/go-kratos/kratos/pull/3856) @ `e4697dc1` | HTTP path escaping | Rework required | Not yet adopted | Escaping `?`, `#`, and spaces is required, but preserving every slash lets a single-segment variable change route structure. Escaping must be AIP-template-aware. |
| [PR #3838](https://github.com/go-kratos/kratos/pull/3838) @ `1a937058` | Eureka endpoint metadata aliasing | Candidate | Not yet adopted | Cloning metadata per endpoint is a focused correctness fix for the retained Eureka integration. |
| [PR #3836](https://github.com/go-kratos/kratos/pull/3836) @ `b444c589` | EWMA cancellation health signal | Rework required | Not yet adopted | The `errors.Is` argument order must be fixed and caller cancellation should not degrade backend health; deterministic tests should replace timing-sensitive assertions. |
| [PR #3852](https://github.com/go-kratos/kratos/pull/3852) @ `1c993d03` | `kratos run` relative paths | Candidate | Not yet adopted | The path normalization is sound, but the upstream test only tests `filepath.Abs` rather than the command behavior. |
| [PR #3781](https://github.com/go-kratos/kratos/pull/3781) @ `bf65393e` | Eureka retry server selection | Rework required | Not yet adopted | Indexing must use `len(urls)`, but zero-retry semantics and retry tests need an explicit contract. |
| [PR #3835](https://github.com/go-kratos/kratos/pull/3835) @ `e98b0686` | gRPC stream timeout | Rejected | None | Applying the default two-second unary timeout to every stream would terminate normal long-lived streams. Any stream timeout must be explicit and cover the full send/receive lifecycle. |
| [PR #3813](https://github.com/go-kratos/kratos/pull/3813) @ `04a41bad` | Opaque protobuf HTTP generation | Rework required | Not yet adopted | The stale patch does not correctly cover scalar, repeated, map, and dotted named fields against the current generator. |

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
