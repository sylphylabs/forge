# HTTP Request-Path Optimization Plan

Status: deferred until the accepted `transport/http/transcoding` boundary lands

Last reviewed: August 9, 2026

## Purpose

This document defines later performance work that Forge can own on the
HTTP request path. It separates framework cost from application work, records
the behavior that optimizations must preserve, and identifies decisions that
must be approved before implementation.

Correct package ownership comes first. The accepted transcoding design compiles
each Google HTTP rule into one immutable
`transport/http/transcoding.Binding`; this plan measures and optimizes that
target, not the current spread of path, ProtoJSON, query, and stream helpers.

This plan complements the broader
[runtime modernization contract](runtime-modernization.md), the existing
[performance record](performance.md), and the
[Google HTTP transcoding contract](google-http-transcoding.md). It is an
implementation backlog, not a release performance claim.

## Boundary

A generated unary HTTP request currently follows this path:

```text
client
  -> operating system, TCP, TLS, and net/http parsing
  -> HTTP filters
  -> route and Google path-template matching
  -> request context and transport metadata
  -> path, query, and body binding
  -> generated middleware execution
  -> application service method
  -> database, cache, or downstream calls
  -> response or error encoding
  -> net/http and the operating system
```

Forge owns the routing, request adaptation, generated binding, middleware
dispatch, built-in middleware, response encoding, and error encoding stages.
It does not own:

- application service logic or algorithms;
- SQL construction, transactions, database drivers, or database latency;
- cache access and cache policy;
- application-owned downstream HTTP, gRPC, or message-queue calls;
- application-defined filter or middleware internals;
- reverse proxies, load balancers, the kernel, or network latency; or
- the internals of Go's `net/http` implementation.

Forge may improve how it invokes user filters and middleware, but it must
not claim improvements from changing the work those components perform.

## Current Baseline

The completed request-binding series from `4f11c9e8` through `6fc25444` removed
avoidable routing, context, path-value, and protobuf-binding work. On Apple M5
Pro, Go 1.27rc2, `darwin/arm64`, and `GOMAXPROCS=1`, the local generated-like
unary benchmark moved approximately as follows:

| Revision | Time | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| `4f11c9e8` | 2,033 ns/op | 2,824 B/op | 35 allocs/op |
| `6fc25444` | 877 ns/op | 1,072 B/op | 15 allocs/op |

This is about 57% less time, 62% fewer bytes, and 57% fewer allocations inside
the measured framework path. The absolute saving is about 1.2 microseconds per
request. It is material for framework overhead and high-throughput lightweight
handlers, but normally negligible beside millisecond-scale storage or network
work.

Before the next optimization lands, these measurements must be promoted into a
repository-owned benchmark report with raw samples, exact commands, and
`benchstat` output. Approximate numbers in this planning document are not a
release claim.

## Compatibility Contract

Unless a decision gate below is explicitly approved, request-path work must
preserve all of the following:

- `net/http` remains the server API and transport implementation.
- Parent context values, cancellation, and deadlines propagate to handlers.
- The request context is canceled when the request handler returns.
- `Request.Pattern`, public `PathValue`, `Context.Vars`, transport operation,
  and path-template values remain unchanged.
- Body, query, and path precedence follows the generated transcoding contract.
- Calling `Bind` leaves the request body readable with the same bytes as today.
- Middleware selection, order, nesting, error propagation, and panic behavior
  remain unchanged.
- Custom filters, middleware, instance-owned codecs, request decoders, response
  encoders, and error encoders continue to observe their documented request and
  response.
- Success status codes, content types, headers, and response-body bytes remain
  unchanged. Errors use the canonical `application/problem+json` contract.
- SSE, WebSocket, `HttpBody`, redirects, and streaming behavior do not inherit
  unary-only optimizations accidentally.
- Open and Opaque protobuf APIs follow the same wire contract.

Moving repeatable work from request time to generation, registration, or server
construction time is preferred. Pooling is not preferred when precomputation or
allocation removal can solve the same problem without adding lifetime rules.

## Measurement First

The next stage begins with a persistent generated-handler benchmark that can
attribute cost to individual framework stages. It must cover:

- route only;
- route plus request context and transport metadata;
- path, query, body, and combined binding;
- no middleware, one middleware, and representative middleware chains;
- response encoding and error encoding;
- Open and Opaque protobuf messages;
- simple, nested, repeated, map, bytes, enum, and field-mask values;
- static, simple parameter, nested field, multi-segment AIP, and custom-verb
  routes;
- empty, small, and representative request and response bodies; and
- serial and parallel execution at documented `GOMAXPROCS` values.

A real TCP benchmark must separately report keep-alive and new-connection
requests, concurrency, payload size, warmup, duration, and CPU limits. Injected
application delays of zero, 50 microseconds, 1 millisecond, and 10 milliseconds
should show how framework savings affect complete request latency.

CPU and heap profiles decide which backlog item proceeds. A microbenchmark must
not be used to infer an end-to-end percentage.

## Direct Work

The following investigations and implementation directions do not intentionally
change application behavior. Each implementation still requires conformance,
race, and before-and-after performance evidence.

### 1. Precompute Immutable Route Facts

The accepted transcoding boundary already requires descriptor interpretation,
field classification, route metadata, and binding plans to be owned once by an
immutable `Binding`. The request path should not rediscover information already
known from the Protobuf descriptor and `HttpRule`.

Candidate work:

- store direct path, query, body, response, and stream-field plans on the
  compiled Binding;
- precompute operation identity and transport metadata shared by handlers;
- avoid repeated field-path splitting and descriptor lookup;
- keep complex AIP extraction on the shared parser without rebuilding
  intermediate maps when the result can be written directly; and
- keep dynamic or reflection-based fallbacks explicit and benchmarked.

This work aligns with the typed, precompiled operation workstream in the runtime
modernization contract. It must not create a second operation model for HTTP.

### 2. Remove Query-Binding Intermediates

The protobuf query path currently materializes `url.Values` and then decodes it.
Investigate a descriptor-aware decoder over `RawQuery` that preserves standard
percent decoding, repeated values, maps, nested messages, field masks, empty
values, and error classification.

Required proof:

- differential tests against the current decoder;
- fuzzing for escaping and malformed input;
- conformance fixtures for Open and Opaque messages; and
- separate measurements for empty, scalar, repeated, map, and nested queries.

The implementation is rejected if it duplicates a large general-purpose query
parser or weakens `net/url` compatibility for a marginal saving.

### 3. Reduce Body Decode Copies

`DefaultRequestDecoder` currently reads the complete body, restores
`Request.Body`, selects a codec, and decodes from the buffered bytes. The body
replay contract is retained by this plan.

Candidate work:

- eliminate redundant buffer growth or byte copies while retaining one stable
  replay buffer for the full request lifetime;
- add bounded-size helpers as part of the separate request-budget design;
- specialize protobuf JSON adapters where descriptor-aware decoding avoids
  temporary messages without resetting unrelated fields; and
- keep `HttpBody`, empty body, malformed body, and custom codec behavior
  identical.

Streaming directly from `Request.Body` is not a direct-work item because it
changes replay behavior and error timing.

### 4. Reduce Response-Encoding Intermediates

Profile generated protobuf JSON responses, projected fields, `HttpBody`, and
errors independently. Prefer direct marshaling into an owned request buffer only
when exact response bytes and error timing can be preserved.

Candidate work:

- avoid redundant `encoding/json` adapter layers for protobuf messages;
- reuse capacity within one response without exposing pooled storage;
- avoid repeated content-type parsing and codec lookup where the generated
  operation fixes the representation; and
- retain the current response body, including its trailing newline where
  applicable.

No response optimization may write headers earlier than today or turn a
recoverable marshal error into a partial successful response.

### 5. Optimize Built-in Middleware Internals

Forge can optimize middleware it owns, but not custom middleware work.
Profile recovery, logging, metadata, validation, tracing, and metrics with each
component disabled, enabled, and sampled out.

Candidate work:

- use fixed-capacity attribute storage where the final size is bounded;
- avoid reflection, string formatting, and stack formatting until needed;
- precompute stable operation attributes;
- avoid provider/exporter work when telemetry is disabled or sampled out; and
- preserve middleware order, context propagation, logging fields, and error
  classification.

Skipping calls to user-defined `String` or `Redact` methods when a log record is
disabled is covered by Decision Q2 because those calls are observable user code.

### 6. Measure Error and Miss Paths Separately

Not-found, method-not-allowed, malformed path, binding error, validation error,
timeout, cancellation, panic, and response-marshal failure are separate paths.
They must not distort success-path claims, but fixed allocation regressions
should not be ignored when errors or misses are operationally common.

Optimize these paths only after profiles or production evidence show material
volume. Correct error status, reason, configured encoder use, and middleware
visibility take precedence over allocation count.

## Decisions Fixed by This Plan

The following choices preserve current user-visible behavior and do not require
further approval for the next stage:

- request-body replay remains available after `Context.Bind`;
- existing response encoders remain byte-exact, including trailing newlines;
- public request contexts and wrappers are not pooled;
- the child request context and its cancellation boundary are preserved; and
- fasthttp or another non-`net/http` transport is out of scope.

Changing any of these later requires a separate proposal because it would alter
application-visible lifetime, wire, cancellation, or transport behavior.

## Approved Behavior Decisions

The following two changes can affect framework users and were approved on
July 23, 2026.

### Q1. When Is Middleware Configuration Final?

Decision: generated service middleware becomes immutable when
`Wrap<Service>HTTPServer` constructs the wrapper. The constructor snapshots the
plan and returns an error for invalid middleware before route registration.
HTTP `Server.Use`, server `Middleware`, `Server.WrapMiddleware`, and
`Context.Middleware` are removed rather than retained behind a freeze boundary.

### Q2. May Disabled Logging Skip User Formatting Hooks?

Decision: yes. When a log level is disabled or a trace is not sampled, built-in
observability may avoid calling user-defined `String`, `Redact`, or equivalent
formatting methods. Disabled observability should not execute expensive
formatting. This must be documented because a user method with side effects
will be called less often, even though such side effects are undesirable.

## Sequencing

The expected next-stage order is:

1. Land the persistent stage-by-stage and real TCP benchmark matrix.
2. Archive the `4f11c9e8` to `6fc25444` request-path result with raw evidence.
3. Precompute immutable route and binding facts.
4. Precompose middleware dispatch in generated wrapper constructors.
5. Optimize query binding with differential and fuzz tests.
6. Optimize body decoding while preserving body replay.
7. Optimize response encoding while preserving exact bytes.
8. Optimize disabled observability paths under the approved Q2 behavior.
9. Revisit error paths, pooling, or alternate transports only with evidence.

Each implementation remains a focused commit or reviewable series. Routing,
middleware ABI, body semantics, response bytes, and transport replacement must
not be changed in one combined rewrite.

## Acceptance Gates

Every landed optimization must provide:

- exact baseline and candidate commits;
- Go version, OS, architecture, CPU, and `GOMAXPROCS`;
- at least ten sequential samples and `benchstat` output;
- `ns/op`, `B/op`, and `allocs/op` for the isolated mechanism;
- full root tests and vet, focused race tests, and generator-module tests;
- generated Open and Opaque fixture coverage when binding changes;
- differential or fuzz tests for attacker-controlled parsers;
- explicit success, error, cancellation, and streaming regression tests where
  relevant; and
- an end-to-end result that states the handler work, payload, concurrency, and
  connection model.

An optimization should normally remove a measured hotspot, at least one steady-
state allocation, or a statistically significant amount of CPU without adding
disproportionate complexity. A smaller change may still land when it simplifies
the implementation or removes a dependency. Faster code that weakens the
compatibility contract does not pass without an approved decision gate.

## Stop Conditions

Stop request-path micro-optimization and move to a different workstream when:

- profiles no longer identify framework-owned request work as material;
- remaining cost is dominated by `net/http`, serialization required by the
  wire contract, custom middleware, or application work;
- an optimization only moves cost to registration while causing unacceptable
  startup time or memory growth;
- the measured end-to-end result is lost in noise for the target workload and
  the change does not simplify the framework; or
- further gains require pooling public request objects or changing transport
  semantics without an approved product reason.

The objective is not a zero-allocation marketing benchmark. It is a small,
predictable, protocol-correct framework tax with evidence that shows where it
matters and where it does not.
