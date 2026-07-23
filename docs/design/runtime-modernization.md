# Runtime Modernization

Status: proposed implementation contract

Last reviewed: July 23, 2026

## Purpose

This document defines the next modernization boundary for OpenKratos. It is a
design contract for later implementation, not a statement of current behavior
and not a speculative feature list. A workstream becomes an OpenKratos
compatibility fact only after its acceptance gates pass and
[`COMPATIBILITY.md`](../../COMPATIBILITY.md) is updated.

OpenKratos is positioned as a standard-library-first, protocol-correct,
embeddable, and verifiable protobuf service runtime. Lower request cost matters,
but "faster Kratos" is not a sufficient product boundary. A router can be much
faster in isolation while producing only a small end-to-end improvement once
serialization, middleware, telemetry, network I/O, and business work dominate a
request. The larger opportunity is to make ownership explicit, move repeatable
work to generation or construction time, and make production behavior testable.

This contract complements the focused
[performance design](performance.md), the
[Google HTTP transcoding contract](google-http-transcoding.md), the
[public Protobuf API contract](public-protobuf-api-module.md), and the
[generated middleware contract](generated-middleware.md). The owned generator
topology is defined by
[`protobuf-generation.md`](protobuf-generation.md). This document does not
replace those focused contracts.

## Goals

- Allow multiple independent OpenKratos applications to run in one process
  without sharing codecs, logging, telemetry providers, signals, or mutable
  framework state.
- Make context cancellation, readiness, shutdown, and background goroutine
  ownership explicit and testable.
- Keep generated protobuf operations typed and precompiled through the common
  path. Legacy middleware migration support stays outside the core path.
- Provide defensible HTTP server and client defaults, with explicit limits and
  per-operation budgets.
- Reuse one protobuf operation model across HTTP, gRPC, telemetry, documentation,
  and optional protocol adapters.
- Keep the root runtime small. New protocol integrations and provider SDKs stay
  optional and must not enlarge the dependency graph of applications that do
  not use them.
- Make every performance, conformance, lifecycle, and release claim
  reproducible from repository-owned tests or reports.

## Non-goals

- Replacing `net/http`, gRPC-Go, protobuf reflection, or OpenTelemetry with
  OpenKratos-owned implementations.
- Introducing a dependency-injection container, service locator, or implicit
  process-wide runtime.
- Rewriting every middleware with generics. Cross-cutting middleware still
  needs a stable operation boundary; typing is applied where it removes
  reflection or invalid states rather than as a language showcase.
- Combining all workstreams into one framework rewrite or one release gate.
- Treating benchmark throughput as a substitute for protocol correctness,
  cancellation behavior, or resource bounds.
- Adding Connect, gRPC-Web, or OpenAPI behavior directly to the root module
  before the shared operation contract is stable.
- Preserving every Kratos v3 source behavior through permanent compatibility
  shims. OpenKratos is pre-v1 and may make documented, migratable breaks.

## Design Principles

1. **Explicit ownership.** The component that starts a goroutine, opens a
   listener, creates a watcher, or installs a provider owns its cancellation and
   waits for its termination.
2. **No hidden globals on the primary path.** Migration tooling may explain or
   rewrite legacy global helpers, but OpenKratos core internals use
   instance-scoped dependencies.
3. **Generate facts and wiring points, configure behavior.** Protobuf descriptors
   and HTTP rules determine operation shape at build time. Generated Go RPC
   fields bind application middleware during construction; middleware names do
   not enter descriptors. Implementations, timeouts, limits, telemetry, and
   deployment policy remain application configuration.
4. **Standard library first.** Use current Go primitives when they provide the
   required semantics. Add an abstraction only when OpenKratos must express a
   cross-transport contract or isolate an optional dependency.
5. **Correctness before hot-path work.** Generated and construction-time work
   may remove steady-state reflection or parsing only after wire behavior is
   covered by conformance tests.
6. **Optional integrations remain optional.** Adapters live in nested modules
   with explicit support and release policy.
7. **Evidence precedes claims.** Proposed behavior stays in design documents;
   accepted behavior moves to compatibility documentation only with executable
   evidence.

## Current Baseline

OpenKratos already differs materially from its Kratos v3 baseline:

- `net/http.ServeMux` owns the HTTP routing tree while a shared parser preserves
  Google path-template behavior.
- Google HTTP transcoding uses one path model across generation, client
  expansion, server registration, and variable extraction.
- Generated HTTP code supports protobuf Editions Open and Opaque APIs.
- Go 1.27 standard-library-backed HTTP/2 is the supported path.
- Application stop is idempotent, lifecycle errors are joined, and after-stop
  hooks receive a fresh bounded context.
- Config reloads publish a complete resolved snapshot atomically.
- Logging is based on `log/slog`, and transport telemetry uses current
  OpenTelemetry semantic conventions.
- Unary and stream middleware have separate runtime contracts, generated
  service plans, and registration-time HTTP/gRPC composition.
- Selector and codec hot paths have controlled before-and-after benchmark
  evidence.

These improvements are retained. The modernization work below addresses
remaining inherited runtime boundaries rather than reopening validated work.

## Remaining Gaps

| Area | Current inherited boundary | Required direction |
| --- | --- | --- |
| Codec ownership | `encoding.RegisterCodec` mutates a package map populated by import-time registration. | Immutable or explicitly owned registries passed to consumers. |
| Logger ownership | Constructing an `App` with a logger changes `slog.Default` for the process. | App-scoped logging; migration tooling rewrites legacy global-helper usage. |
| Application lifecycle | `App.Run` installs process signals and combines host policy with service lifecycle. | Context-first application control; signal handling is an opt-in host concern. |
| Readiness | Server goroutine start is used as the registration barrier. It does not prove that every server is ready to accept traffic. | Explicit readiness state and registration only after all required servers are ready. |
| HTTP protection | The constructed `http.Server` sets a handler and TLS config but no header, idle, or header-size limits. | Documented secure defaults, explicit opt-outs, and streaming-aware request budgets. |
| Config watchers | `Config.Load` starts retrying watcher goroutines; `Close` stops watchers but does not explicitly join every loop. Source APIs do not accept contexts. | Context-aware load/watch APIs with deterministic close and retry ownership. |
| Protocol surface | Core transports are HTTP and gRPC; browser and schema consumers need separate integration work. | Optional adapters built from the same generated operation description. |
| Telemetry configuration | Some paths still default to global OpenTelemetry providers and middleware owns parts of operation interpretation. | Injected providers and a stable, cardinality-bounded operation contract. |
| Release topology | The repository contains 26 Go modules with temporary local replacements before the first release. | A machine-readable release inventory, support tiers, and external-consumer validation. |

The presence of reflection, allocations, middleware, or telemetry does not by
itself indicate waste. Most of those operations implement necessary behavior.
Optimization work must distinguish required business or protocol work from
avoidable repeated discovery, parsing, lookup, locking, and allocation.

## Workstream 0: Release Baseline

The local atomic generator cutover may proceed before public release. Publishing
the first supported root and API versions plus the atomic Buf plugins remains a
prerequisite for claiming a releaseable external toolchain. Existing Google HTTP
transcoding and error generation must be available through pinned published
plugins without repository-relative replacements before public release.

Required outcomes:

- Publish a machine-readable inventory of every module, owner, support tier,
  dependency order, and tag prefix.
- Remove local `replace` directives from published artifacts.
- Publish and consume pinned `buf.build/openkratos/go-errors`, `go-http`, and
  `go-middleware` plugin revisions outside the repository.
- Build and test a minimal external consumer using only published versions.
- Record the root, generator, and supported contrib version relationship.

This workstream changes packaging, not runtime behavior. It must not be bundled
with the typed operation redesign.

## Workstream 1: Explicit Runtime Dependencies

OpenKratos must provide a fully instance-scoped path for codecs, logging, and
telemetry. An application must be able to construct all transports without
mutating `slog.Default`, the OpenTelemetry globals, or a package codec map.

The detailed API requires a focused proposal, but it must satisfy these
constraints:

- Codec registration produces an owned registry that is immutable after the
  application or transport is built.
- Built-in codecs are available through an explicit constructor, not only
  through blank imports and `init` side effects.
- Servers, clients, config decoders, and stream codecs receive the same owned
  registry when they belong to one application.
- `App` retains its logger as a dependency and never calls `slog.SetDefault`.
- Trace and metric providers and text-map propagation can be injected. Explicit
  `nil` or omitted options have documented behavior.
- New core code and generated code do not call package-level default-runtime
  helpers. Migration tooling rewrites known legacy helper usage, and any
  temporary adapter lives outside the core module.
- Runtime construction validates duplicate codec names and invalid dependency
  combinations before listeners or goroutines start.

Acceptance gates:

- Two applications with conflicting codec names, loggers, and telemetry
  providers run concurrently without cross-observation.
- Construction and request handling pass race detection while other goroutines
  construct independent applications.
- Tests do not depend on order, blank-import side effects, or resetting a
  process global between cases.
- A migration example covers custom codecs and application logging.

## Workstream 2: Typed, Precompiled Operations

Generated code should perform descriptor interpretation, field classification,
path parsing, and handler adaptation once during generation or registration.
The successful request path should invoke a concrete request/response handler
without reflective method discovery.

The shared operation description must include at least:

- Fully qualified service and method names.
- Transport operation name and canonical route template.
- Request and response protobuf descriptors where runtime reflection is
  genuinely required.
- Streaming shape, idempotency level, and body/query/path projections.
- Stable operation identity for telemetry and generated Go middleware fields.

Implementation constraints:

- Generated service bindings expose a typed internal entry point for each
  method.
- Migration documentation maps existing `middleware.Middleware` values to
  `middleware.UnaryMiddleware` and generated service plans. No runtime adapter
  or compatibility alias remains in core code.
- Middleware names and execution policy do not enter Protobuf descriptors.
  Generated plans expose RPC fields and generated wrappers compose them before
  registration without runtime string dispatch.
- Unary and stream middleware use separate contracts. Stream middleware wraps
  one lifecycle and decorates the stream for per-message behavior.
- HTTP, gRPC, documentation generation, and optional adapters consume the same
  operation facts. No adapter maintains a second parser for `HttpRule`.
- Reflection fallback, if retained for dynamic services, is explicit and
  benchmarkable rather than silently selected.

Acceptance gates:

- Open and Opaque protobuf fixtures exercise the typed path for HTTP and gRPC.
- Generated client/server version mismatches fail with an actionable build or
  startup error.
- Middleware migration tooling has behavior tests covering ordering, errors,
  and context propagation without adding a selector path to the core runtime.
- Benchmarks isolate registration cost, steady-state handler dispatch,
  allocations, and any migration-only adapter measured outside the core path.
- Fuzz or descriptor-driven tests prove that operation metadata is identical
  across transport consumers.

## Workstream 3: Context-First Lifecycle and Readiness

`App` coordinates service lifecycle; it should not decide how the host process
receives shutdown signals. Cancellation from a caller-owned context becomes the
primary control path. A small opt-in helper may use `signal.NotifyContext` for
command binaries.

Required semantics:

- Starting an app accepts or is bound to a caller-owned context.
- Process signal handling is outside the application core and importing the
  root package does not install signal handlers.
- Every standard transport reports one of starting, ready, stopping, stopped,
  or failed through an explicit lifecycle boundary.
- Service registration occurs only after all required transports are ready.
- A startup or readiness failure cancels and joins every component already
  started; it never leaves a listener, registration, or goroutine behind.
- Shutdown is idempotent, bounded, and preserves all stage errors.
- Readiness and liveness are distinct. Readiness becomes false before
  deregistration and draining; liveness remains a host policy.
- Hooks receive the application context and a bounded shutdown context as
  appropriate. Hooks do not receive an already-canceled context accidentally.

Acceptance gates:

- Tests cover cancellation before start, cancellation during start, partial
  readiness, registration failure, repeated stop, timeout, and concurrent stop.
- Standard HTTP and gRPC servers prove listener readiness rather than goroutine
  entry.
- Leak checks and race detection cover every failure stage.
- An embedding test runs two apps under independent parent contexts without
  signals or shared shutdown.

## Workstream 4: HTTP Safety and Request Budgets

OpenKratos should make a safe HTTP service straightforward without imposing
incorrect deadlines on streams. Exact default values require a separate table
and behavior/migration review before implementation.

The design must cover:

- Server `ReadHeaderTimeout`, `IdleTimeout`, `MaxHeaderBytes`, and graceful
  drain behavior.
- Explicit maximum encoded and decoded request body sizes.
- Client TLS handshake, response-header, idle-connection, and connection-pool
  policy without replacing user-supplied `http.Transport` behavior.
- Per-operation unary request budgets selected from generated operation
  identity.
- Explicit streaming policy. SSE, WebSocket, and other long-lived streams do
  not inherit a unary wall-clock timeout.
- Trusted-proxy and forwarded-header policy that is opt-in and testable.
- Clear zero-value semantics and explicit escape hatches for deployments that
  terminate or enforce limits at another layer.

The existing generic `Timeout` behavior must not be silently repurposed. Its
migration must state whether it maps to a unary operation budget, is deprecated,
or is removed before v1.

Acceptance gates:

- Slow-header, oversized-header, oversized-body, slow-body, idle-connection,
  and shutdown tests prove each bound.
- Unary timeout tests cover cancellation at the handler and client.
- Streaming conformance proves that supported streams remain open beyond a
  unary budget and still close on parent cancellation or explicit deadlines.
- Defaults and opt-outs appear in behavior and migration documentation.

## Workstream 5: Config and Provider Lifecycle

Config loading and watching become context-aware. The coordinator owns retry
and backoff policy; providers only report source events and errors.

Required semantics:

- Load and watch operations accept contexts.
- Watch cancellation unblocks `Next` and provider I/O promptly.
- `Close` is idempotent, cancels watchers, waits for coordinator goroutines, and
  joins provider errors.
- Retry backoff, jitter, and terminal-error classification are configured at
  the coordinator rather than duplicated across providers.
- Reload continues to publish one fully resolved atomic snapshot.
- Observer delivery has documented ordering, concurrency, panic, and slow
  consumer behavior.
- Providers do not log through process globals and do not start unowned
  goroutines.

Acceptance gates:

- A common provider conformance suite runs against file and supported remote
  providers.
- Tests cover cancellation during load, blocked watch, repeated close, retry,
  invalid snapshots, observer reentry, and provider failure.
- Race and leak checks run against reload and close concurrently.
- Migration examples cover custom `Source` and `Watcher` implementations.

## Workstream 6: Optional Modern Protocol Adapters

Connect, gRPC-Web, and OpenAPI are valuable only if they reuse the protobuf
operation contract instead of creating parallel routing and binding systems.
Each integration requires its own approved design and nested module.

Candidate boundaries:

- A Connect adapter may expose Connect, gRPC, or gRPC-Web-compatible handlers
  using an established upstream implementation. OpenKratos must not implement
  those wire protocols from scratch.
- Asynchronous message adapters implement the small
  [`transport/message`](../../transport/message) contract. Broker SDKs,
  acknowledgement semantics, retry policy, and delivery-specific fields stay
  in nested modules; adapters do not add a second operation parser or global
  provider registry.
- OpenAPI 3.2 generation is a build-time artifact derived from protobuf
  descriptors, `google.api.HttpRule`, validation annotations, and the shared
  operation model. It is not runtime reflection over registered handlers.
- Generated schemas and handlers use the same path, body, response projection,
  and custom-method semantics as OpenKratos HTTP transcoding.
- Adapter dependencies do not enter the root module dependency graph.
- Unsupported streaming, metadata, error, or content-type behavior fails
  explicitly rather than degrading silently.

Acceptance gates:

- Every adapter passes upstream protocol conformance where a suite exists.
- Cross-transport fixtures prove operation names, error mapping, cancellation,
  metadata, and JSON behavior.
- OpenAPI artifacts have deterministic golden tests and validate with the
  official OpenAPI 3.2 schema and an independent parser.
- A browser-oriented external consumer proves the supported gRPC-Web or Connect
  path without a framework-specific proxy.

No adapter is a blocker for the root runtime release.

## Workstream 7: Stable Telemetry Contract

Telemetry is part of the public operational surface. OpenKratos must define
stable operation naming and bounded attributes before adding more instruments.

Required semantics:

- Trace, metric, and log correlation use the same generated operation identity.
- Route templates, not concrete paths or user identifiers, populate metric and
  span names.
- Transport-specific semantic-convention attributes are versioned and tested.
- Providers and propagators are injectable through the explicit runtime.
- Instrument names, units, buckets, error classification, and cardinality
  limits are documented.
- Telemetry can be disabled without changing handler behavior or retaining
  unnecessary allocations.
- Semantic-convention upgrades include a migration note when emitted names
  or attributes change.

Acceptance gates:

- In-memory exporters assert exact spans, metrics, attributes, status, and
  propagation for HTTP and gRPC.
- Cardinality tests send arbitrary paths, headers, and errors and prove that
  unbounded values are not promoted into metric dimensions.
- No-provider and sampled-out benchmarks report their allocation cost.
- Two applications with different providers remain isolated in one process.

## Workstream 8: Module and Release Governance

The current multi-module repository isolates optional SDK dependencies, but 27
modules create real versioning and validation cost. Module count is not itself
the target; each boundary must justify its release and dependency isolation.

Required policy:

- Classify modules as core, official tool, official integration, or community
  integration, with an owner and support level.
- Maintain one release manifest that records module paths, dependency order,
  compatible root versions, and required external smoke tests.
- Automate checks for stale local replacements, mismatched OpenKratos versions,
  missing tags, and modules skipped by root-only tests.
- Consolidate modules only when they share lifecycle and release cadence and
  doing so does not force large provider SDKs into unrelated dependency graphs.
- Permit an integration to release independently when its upstream SDK requires
  it, while documenting the supported root version range.
- Produce a release bill of materials and provenance for generated tools and
  official integrations.

Acceptance gates:

- One command validates every supported module from a clean checkout.
- Release planning can derive a topological tag order from the manifest.
- External consumers test the root, generators, and at least one integration
  without workspace files or local replacements.
- CI reports an omitted or unsupported module as an explicit decision, not an
  accidental green build.

## Sequencing

Workstreams are intentionally independent. The expected order is:

1. Complete the release baseline and atomic Buf plugin publication.
2. Introduce explicit runtime dependencies without changing operation binding.
3. Redesign application lifecycle and config lifecycle as separate changes.
4. Land the shared generated operation contract, generated Go middleware plans,
   and direct registration-time composition.
5. Add HTTP safety defaults and per-operation budgets using that contract.
6. Stabilize telemetry on the same operation identity.
7. Evaluate optional protocol adapters one at a time.
8. Apply module governance throughout; consolidate only with dependency and
   release evidence.

A workstream may prepare private implementation details needed by the next one,
but it must remain independently reviewable and revertible. No change should
simultaneously replace global state, middleware, lifecycle, and generated code.

Before implementation begins for a workstream, its focused proposal must freeze
public API signatures, default values, resulting behavior, and migration
examples that this document intentionally leaves open.

## Migration Rules

- Current shipped behavior remains documented by `COMPATIBILITY.md`, but the
  new core design is not required to preserve Kratos source compatibility.
- Every source or behavior break adds an executable or mechanically precise
  migration step under `docs/migration/`.
- Legacy adapters belong in migration-only packages or tools. New core code
  does not depend on them.
- Generated-code changes define the supported generator/runtime version matrix
  and fail clearly outside it.
- Wire compatibility is preserved unless a protocol conformance bug requires a
  documented correction.
- Optional adapters never change the behavior or dependency graph of the core
  HTTP and gRPC transports when they are not imported.
- Secure-default changes require both migration instructions and explicit
  opt-out behavior.

## Validation Contract

Every workstream chooses the relevant checks below and records exact commands,
toolchain, operating system, and commits.

### Correctness and Conformance

- Focused unit, integration, and generated-fixture tests.
- Race detection for affected root and nested modules.
- Fuzzing for parsers, generated metadata, and boundary decoders where input is
  attacker-controlled.
- Goroutine and resource leak checks for lifecycle work.
- Upstream protocol conformance plus independent parsers or consumers for
  adapter and schema work.
- External-consumer builds without `go.work` or local `replace` directives.

### Performance

Microbenchmarks must isolate the changed mechanism and report `ns/op`, `B/op`,
and `allocs/op` with multiple samples and `benchstat`. End-to-end benchmarks
must separately cover:

- A minimal generated handler to expose framework overhead.
- Representative protobuf JSON and binary payloads.
- Middleware and telemetry disabled, enabled, and sampled out.
- Serial and parallel execution at documented `GOMAXPROCS` values.
- Success, validation error, not-found, cancellation, and timeout paths.
- A realistic handler with downstream or storage latency to show how much a
  framework improvement changes the complete request.

Report throughput and latency percentiles only with the load model, connection
reuse, concurrency, payload, duration, warmup, and resource limits stated.
Compare against the previous OpenKratos commit on identical hardware. Upstream
Kratos, plain `net/http`, Echo, Connect, or other frameworks may be informative
comparisons, but they are not acceptance baselines unless behavior and enabled
features are equivalent.

No fixed nanosecond or requests-per-second threshold is a CI correctness gate.
CI verifies benchmark compilation and regression-sensitive invariants;
controlled runs provide release evidence.

## Definition of Done

A modernization workstream is complete only when:

- Its API and behavior match an approved focused proposal.
- Correctness, race, lifecycle, and conformance gates relevant to the change
  pass.
- Performance claims have reproducible before-and-after evidence and state the
  limits of the result.
- External-consumer or generated-artifact tests cover the public boundary.
- Design, migration, development, and release documentation are updated
  together.
- No temporary global fallback, local replacement, experimental dependency, or
  unowned goroutine is left undocumented.

This definition keeps OpenKratos differentiation grounded in observable
runtime properties: explicit isolation, protocol correctness, safe defaults,
predictable lifecycle, optional integrations, and evidence-backed cost.
