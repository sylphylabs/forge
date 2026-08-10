# Forge Compatibility with Forge

Status: pre-release

Last verified: August 9, 2026

Forge is an independent fork of `go-kratos/kratos`. It is not a drop-in
replacement for Kratos v3 and does not promise source, behavior, or release
compatibility with future Forge versions.

Forge also does not retain a Forge API solely for compatibility. An API
may be removed when a clearer or more efficient replacement is available and
the change is technically justified. Such removals are intentional breaking
changes and must ship with executable migration guidance.

This document is the source of truth for intentional differences that have
been accepted and validated in Forge. Work in progress is not a
compatibility fact. Proposed and rejected differences belong in
[`docs/upstream-adoptions.md`](docs/upstream-adoptions.md).

## Comparison Baseline

- Upstream repository: `https://github.com/go-kratos/kratos`
- Initial upstream commit: `668db92c2c001e9552594ba5a8aede8456af6d7e`
- Initial upstream release line: Kratos v3
- Forge release line: v0, currently unreleased

The baseline commit matters because upstream `main` continues to change. An
entry below describes a difference from that baseline unless it links a newer
upstream revision explicitly.

## Compatibility Summary

| Area | Kratos v3 baseline | Forge | Impact |
| --- | --- | --- | --- |
| Root module | `github.com/go-kratos/kratos/v3` | `github.com/sylphylabs/forge` | Source breaking |
| Release line | v3 | v0 pre-release | Release breaking |
| Minimum Go version | Go 1.25 | Go 1.27 | Build requirement |
| Module count | 28, including `cmd/forge` | 27 | Release and tooling change |
| Asynchronous message transport | No protocol-neutral async contract | Root module exposes `transport/message`; broker SDK adapters remain optional nested modules | New API; no broker wire compatibility claim |
| Project CLI | `cmd/forge` | Removed | Workflow breaking |
| Protobuf generators | Forge module paths | Forge module paths | Install path change |
| Contrib provider SDKs | Older provider majors and archived direct dependencies | Current stable majors and standard maintained replacements | Source and dependency graph change |
| UUID generation | `google/uuid` and `gofrs/uuid` | Standard-library `uuid` | Source and generated-ID behavior change |
| HTTP protobuf generation | Open API field access | Editions 2023 Open and Opaque API accessors | New generated-code capability |
| Google HTTP transcoding | Partial, independently parsed route/body/query behavior | Shared path grammar, strict generation, ProtoJSON projection, and additional bindings | API and wire behavior breaking |
| HTTP router | Gorilla mux | Standard-library `http.ServeMux` tree | Behavior breaking |
| HTTP client paths | Endpoint base paths and escaped variables could be lost | Base paths and AIP-aware escaping are retained; generated clients reuse compiled path plans | Correctness, generated-code, and performance change |
| HTTP middleware setup | Middleware could be changed while requests were being served | Configuration freezes at first `Start` or `ServeHTTP`; generated unary handlers precompose their chain | Behavior and generated-code change |
| Unknown HTTP routes | Could fall through to `http.DefaultServeMux` | Explicit 404/405 handling | Behavior and security change |
| HTTP streams | Server request timeout could cancel SSE/WebSocket streams | Request timeout is detached; explicit stream deadlines remain | Behavior change |
| WRR selector | Scans a node set during steady-state cleanup | Detects stale entries in O(1) before cleanup | Performance only |
| P2C selector | Per-balancer locked random source | Concurrent `math/rand/v2` top-level source | Performance only |
| App shutdown | Repeated stop and stage errors were not fully defined | Idempotent stop, joined errors, bounded after-stop stage | API and behavior change |
| Transport capabilities | `transport.Server` plus `Endpointer` | Optional `Healthzer` and `GracefulStopper` interfaces; App prefers draining and aggregates readiness | New API |
| Config watch | Sources could be observed in a partially reloaded state | Complete resolved snapshots are published atomically | Behavior change |
| OTel attributes | Legacy semconv and mixed transport attributes | semconv v1.41 transport-specific attributes | Telemetry schema change |
| OTel metrics | Generic unary middleware with custom names, `code`, and `reason` | HTTP semconv v1.41 duration histograms and grpc-go A66 duration metrics | Source and telemetry schema breaking |
| Error classification | HTTP status code stored on the error and mapped to gRPC | Transport-neutral `Kind` projected one way onto each transport | Source, wire, and behavior breaking |
| Error construction | `errors.BadRequest(reason, msg)` and generated `ErrorXxx(format, args...)` | `errors.New(Kind)` and generated sentinel values | Source and generated-code breaking |
| Error matching | Generated `IsXxx(err)` comparing struct fields | `errors.Is` against a generated sentinel, plus `errors.KindOf` | Source breaking |
| Error annotations | `default_code` and `code` carrying HTTP status | `default_kind` and `kind` carrying a `Kind` | Protobuf contract breaking |
| Retry judgment | `errors.IsRetryable` and `Kind.Retryable` classified a Kind in isolation | Removed; the decision needs delivery evidence or an idempotence declaration | Source breaking |
| Client retry default | `KindUnavailable` was retried unconditionally | Retried only with `transport.WasNotSent` evidence or `retry.Idempotent(ctx)` | Behavior breaking |
| Aggregate errors | `errors.Join` silently dropped all but the first on the wire | Explicit `errors.Violations` projected onto `errdetails.BadRequest` | New API |
| Error response format | Negotiated: JSON spelled `NOT_FOUND`, ProtoJSON spelled `KIND_NOT_FOUND` | One `application/problem+json` shape, not negotiated | Wire breaking |
| Error disclosure | Every message crossed the boundary verbatim | Only `errors.Public` crosses: what the caller declared, never a cause | Source and behavior breaking |
| Load-balancing policy | Process-global `selector.SetGlobalSelector`, default set by whichever transport linked first | Per-client `WithSelector`, `wrr` default owned by each transport | Source breaking |
| HTTP client streams | One `ClientStream` interface; a receive-only stream answered its send methods with a runtime error | Core `ClientStream` plus a `SendingClientStream` capability | Source breaking |
| Error response reader | Any body was parsed, unbounded, whatever the status said | Problem media type only, 64 KiB cap, a body contradicting the status is discarded | Behavior change |
| HTTP/gRPC status conversion | `transport/http/status` converted codes in both directions | Removed; each transport projects a `Kind` one way | Source breaking |
| Protobuf in the HTTP transport | `transport/http` always linked the Protobuf runtime | Moved to `transport/http/transcoding`; a plain-JSON service links neither Protobuf nor gRPC | Source breaking; import change for generated code |
| Codec registration | `transport` blank-imported every codec | Registers only the schema-free codecs; `transcoding` registers the Protobuf ones | Behavior change for unregistered content types |

## Repository Identity and Versions

Every Forge module and generated package uses the
`github.com/sylphylabs/forge` prefix. Forge is on the v0 release line, so
its module paths do not carry the upstream `/v3` suffix.

Nested contrib and generator modules follow the same rule. For example:

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/sylphylabs/forge/contrib/middleware/jwt

github.com/go-kratos/kratos/cmd/protoc-gen-go-http/v3
github.com/sylphylabs/forge/cmd
```

Until the first release, local development uses `v0.0.0` requirements and
repository-relative `replace` directives between modules. Published releases
must tag every released nested module with its module-prefixed tag; a root tag
does not release nested modules.

## Toolchain

Forge requires Go 1.27. Until the final toolchain is published, development
and CI use Go 1.27 RC2. The upstream baseline requires Go 1.25. Projects that
must remain on Go 1.25 or Go 1.26 cannot migrate to Forge without upgrading
their toolchain.

The repository currently contains 27 Go modules. Running `go test ./...` at the
root does not test nested modules; use the repository commands documented in
[`DEVELOPMENT.md`](DEVELOPMENT.md).

## Project CLI and Code Generation

Forge does not ship the general `forge` CLI. The removed module included
project scaffolding, source generation wrappers, an application runner, an
upgrader, and a changelog helper.

The removal is intentional. The upstream scaffold copied a template and
performed unrestricted byte replacement across copied files. That replacement
could mutate an embedded protobuf raw descriptor without updating its encoded
length, producing an initialization panic. Forge does not preserve or
repair that implicit source rewriting workflow.

Use explicit tools instead:

| Removed command | Replacement |
| --- | --- |
| `forge new` | Create a normal Go module or use an auditable repository template |
| `forge run` | `go run ./cmd/server -conf ./configs` |
| `forge proto ...` | A repository-owned Buf or `protoc` configuration |
| `forge upgrade` | `go get`, `go install`, and `go mod tidy` |
| `forge changelog` | Git history and GitHub release notes |

Four atomic Forge Protobuf commands share one deterministic source module:

```text
github.com/sylphylabs/forge/cmd/protoc-gen-go-errors
github.com/sylphylabs/forge/cmd/protoc-gen-go-http
github.com/sylphylabs/forge/cmd/protoc-gen-go-message
github.com/sylphylabs/forge/cmd/protoc-gen-go-middleware
```

Each command declares protobuf Editions support through Edition 2024 and emits
only its owned `_errors.pb.go`, `_http.pb.go`, `_message.pb.go`, or
`_middleware.pb.go` artifact.
There is no `protoc-gen-go-forge` forwarding command. Real `protoc`
fixtures compile and execute Edition 2023 Open and Opaque APIs for message,
scalar, repeated, map, explicit-presence, and oneof fields.

Inline unary `google.api.HttpRule` bindings use one shared Google path-template
implementation across generation, client expansion, ServeMux registration, and
server extraction. The primary binding defines the generated Go client method;
all one-level `additional_bindings` are registered as alternative server routes.
Nested additional bindings fail generation.

Google-transcoded JSON uses the public media type `application/json` with
protobuf JSON semantics. Whole messages and named `body`/`response_body`
projections preserve enum names, standard base64 bytes, quoted 64-bit integers,
non-finite floats, messages, repeated fields, maps, and Open/Opaque APIs.
`google.api.HttpBody` continues to carry its declared media type and bytes.

Request fields are classified once: path fields are omitted from body and
query, named-body descendants are omitted from query, and `body: "*"` emits no
query. Nested query leaves and repeated scalar keys are supported; maps and
repeated messages in query position fail generation. The generated server
applies body, query, then path so the URL path remains authoritative.

Invalid path fields, invalid body projections, `response_body: "*"`, duplicate
or conflicting bindings, and ambiguous custom declarations fail generation
without emitting a partial file. Omit `response_body` to encode the whole
response.

Generated HTTP files require `transport/http.SupportPackageIsVersion5`.
Version 4 introduced a concurrency-safe `CompiledPath` for each fixed client
binding; version 5 additionally precomposes unary server middleware. Template
parsing, descriptor validation, middleware matching, and middleware handler
composition are therefore removed from their respective steady-state request
paths. The obsolete version 3 and 4 sentinels are removed: generated HTTP files
must be regenerated with the current generator before upgrading the runtime.

`transport/http.BuildPath` returns `(string, error)` and remains the convenience
API for genuinely dynamic templates. Hand-written code that repeatedly uses a
fixed template should compile it once with `CompilePath` or `MustCompilePath`
and call `CompiledPath.Build` for each request. Generated and hand-written
clients propagate expansion errors before network I/O. A primary
`custom.kind: "*"` returns `ErrUnspecifiedHTTPMethod`; a primary bare `*` or
`**` path returns `ErrUnboundPathWildcard`. Servers and raw HTTP clients may
still use both rule forms.

External `google.api.Service` configuration and
`fully_decode_reserved_expansion` are not implemented; they remain the
separate Phase 2 described in
[`docs/design/google-http-transcoding.md`](docs/design/google-http-transcoding.md).

## Generated Server Middleware

The ambiguous `middleware.Handler`, `middleware.Middleware`, and
`middleware.Chain` names are replaced by `UnaryHandler`, `UnaryMiddleware`, and
`ChainUnary`. Streaming uses the separate `ServerStream`, `StreamHandler`,
`StreamMiddleware`, and `ChainStream` lifecycle contract.

HTTP and gRPC server selector middleware is removed. `http.Middleware`,
`grpc.Middleware`, `grpc.StreamMiddleware`, `Server.Use`,
`Server.WrapMiddleware`, and `http.Context.Middleware` have no compatibility
aliases. Regenerated code exposes a service-specific middleware plan and
`Wrap<Service>HTTPServer` / `Wrap<Service>GRPCServer` constructors instead.
Those constructors snapshot and compose all service and method middleware
before registration; no selector lookup or chain construction remains on the
request path.

`middleware/selector.Server` is also removed. `middleware/selector.Client`
remains available because generated server plans do not replace client-side
operation selection. Use HTTP filters and gRPC interceptors for
transport-native server behavior. Other client middleware options remain
separate and are not affected by this server API replacement.

The isolated before-and-after measurements are recorded in
[`docs/benchmarks/http-middleware-2026-07-23.md`](docs/benchmarks/http-middleware-2026-07-23.md).

## Contrib Provider Dependencies

Forge contrib modules use the current stable Apollo v5, Consul API v2, and
Nacos SDK v2 module paths. This is source breaking for applications that
construct the Consul or Nacos providers with SDK types:

```text
github.com/hashicorp/consul/api
github.com/hashicorp/consul/api/v2

github.com/nacos-group/nacos-sdk-go/clients/config_client
github.com/nacos-group/nacos-sdk-go/v2/clients/config_client
```

Direct dependencies on archived `json-iterator/go`, `golang/mock`, and PGV were
removed in favor of `encoding/json`, `go.uber.org/mock`, and Protovalidate test
fixtures. Requests implementing `Validate() error` are validated through that
method, whether it is hand-written or produced by an external generator, and
doing so does not require the PGV runtime module.

Some current provider SDKs still compile archived transitive packages. Their
ownership and replacement conditions are documented in
[`docs/dependency-maintenance.md`](docs/dependency-maintenance.md); Forge
does not claim that all transitive repositories are maintained.

## UUID Generation

Forge uses Go 1.27's standard-library `uuid` package for all UUIDs it
generates directly. The root module and contrib providers no longer directly
depend on `github.com/google/uuid` or `github.com/gofrs/uuid`.

Resolver selector keys and provider instance IDs remain UUIDv4. The default
application instance ID changes from Google UUIDv1 (`google/uuid.NewUUID`) to
the standard package default (`uuid.New`), which is UUIDv4 in Go 1.27. Services
that require a stable ID or a specific UUID version must continue to provide an
explicit application ID.

Provider SDKs may still carry their own UUID packages transitively. Forge
does not expose those types or add a second direct UUID implementation to hide
that upstream dependency.

## HTTP Routing

Forge replaces `github.com/gorilla/mux` with a routing tree built on the
standard library's `http.ServeMux`. Generated Google AIP paths are compiled to
standard-library method and path patterns. Matched variables remain available
through both `transport/http.Context.Vars()` and `http.Request.PathValue()`.

The public differences are:

- Literal and more-specific patterns win according to `http.ServeMux`
  precedence, independent of registration order.
- Conflicting patterns panic during registration instead of silently selecting
  the first registered route.
- AIP variables, terminal `**` wildcards, terminal custom verbs, and
  single-segment legacy regular expressions are supported.
- Arbitrary Gorilla regular expressions spanning multiple path segments are
  rejected. Express the route as an AIP template instead.
- The inherited no-op `StrictSlash` option is removed. Delete it from server
  construction; path cleaning and trailing-slash redirects use `http.ServeMux`
  behavior.
- `HandlePrefix` uses path-segment prefix semantics, not arbitrary string-prefix
  matching.
- Unknown routes do not fall through to the process-wide
  `http.DefaultServeMux`. Pass it explicitly to `NotFoundHandler` if that
  behavior is required.
- A method mismatch uses the configured method-not-allowed handler rather than
  relying on Gorilla's matcher order.
- Single-segment path variables percent-encode slash and URL delimiters;
  structural slashes are retained only for AIP multi-segment templates.
- Multi-segment variables preserve `%2F` and `%2f` exactly during server
  extraction; single-segment variables fully decode them.
- Direct HTTP client endpoints retain their configured base path. Discovery
  service names remain separate from endpoint path prefixes.

Router benchmarks and acceptance criteria are documented in
[`docs/design/performance.md`](docs/design/performance.md).

## HTTP Streaming

SSE and WebSocket server streams preserve values from the request context but
detach its deadline and cancellation. A unary HTTP server timeout therefore
does not terminate a long-lived stream. Stream lifetime is controlled by I/O
errors, connection closure, and explicit `SetReadDeadline` or
`SetWriteDeadline` calls.

This differs from the upstream baseline's uniform request-timeout behavior. It
also means a stream that requires a maximum lifetime must set that policy
explicitly; the server's unary timeout is not a stream lifetime limit.

## Selector Performance

The selector APIs and selection results are retained, but two implementations
have different steady-state costs:

- WRR compares the current-weight map size with the active node count and only
  builds a cleanup set when stale entries can exist.
- P2C uses concurrency-safe `math/rand/v2` top-level functions, removing the
  per-balancer random-number mutex.

The adopted upstream revisions, invariant tests, and controlled benchmark
results are recorded in
[`docs/benchmarks/selectors-2026-07-22.md`](docs/benchmarks/selectors-2026-07-22.md).

## Application Lifecycle

`App.Stop` is idempotent. Before-stop, deregistration, server-stop, and
after-stop stages continue independently where safe, and multiple failures are
returned through `errors.Join` instead of later failures hiding earlier ones.
`AfterStopTimeout` configures the bounded after-stop stage; its default is ten
seconds. After-stop callbacks preserve application-context values without
inheriting its cancellation.

`transport.Healthzer` and `transport.GracefulStopper` are optional capability
interfaces alongside `transport.Server`. During shutdown, `App` drains a
server that implements `GracefulStopper` within `StopTimeout` and falls back
to `Stop` when the drain is abandoned or fails; a server implementing only
`Start` and `Stop` receives the same single `Stop` call as before.
`App.Healthz` reports whether every server that implements `Healthzer` accepts
new work, and `transport/http/healthz.NewHandler` serves that as an HTTP
readiness probe without registering any route automatically. The gRPC
server's internal health service reports `NOT_SERVING` until `Start` resumes
it.

## Config Reload

A watch event rebuilds all configured sources, resolves cross-source
placeholders, and atomically publishes one complete reader snapshot. Readers no
longer observe a mixture of old and new source values during reload. File
watchers silently skip hidden files rather than surfacing them as reload errors.

## Observability

OpenTelemetry tracing uses semantic conventions v1.41. HTTP and gRPC attributes
are emitted separately, peer ports are integers, gRPC methods are validated,
and invalid original method strings remain available for diagnosis. The former
custom `rpc.status_code` field is now `forge.error.kind` and
`forge.error.reason`, avoiding collision with standard RPC semantic attributes
and naming the failure rather than numbering it.

Metrics are transport-native and duration-only. HTTP uses the stable v1.41
`http.server.request.duration` and `http.client.request.duration` histograms.
gRPC uses grpc-go's gRFC A66 `grpc.client.call.duration`,
`grpc.client.attempt.duration`, and `grpc.server.call.duration`; Forge does
not also emit RC `rpc.*` or legacy generic metrics. Providers are mandatory,
instance-scoped dependencies. SDK readers, exporters, resources, Views,
cardinality limits, and exemplar policy remain application-owned.

The inherited `metrics.Server`, `metrics.Client`, generic instrument options,
default instrument/View helpers, and process-environment exemplar helper are
removed without compatibility shims. HTTP instrumentation now covers the
native `ServeHTTP` and `RoundTrip` lifecycles instead of unary middleware, and
grpc-go owns unary, streaming, retry, and hedging lifecycles. The complete
contract and cardinality policy are defined in
[`docs/design/otel-metrics.md`](docs/design/otel-metrics.md).

## Metadata Propagation Encoding

`middleware/metadata` percent-escapes a propagated value that cannot travel as
a header value, and unescapes it on the receiving side. gRPC admits only
printable ASCII, 0x20 through 0x7E, in a non-binary header and fails the whole
RPC with an `Internal` error otherwise, so under the Kratos v3 behavior any
metadata carrying a non-ASCII name, address, or control byte terminated the
call before it reached the server.

Escaping engages only when a value falls outside that range or contains `%`,
the escape marker itself. A value of printable ASCII without `%` is transmitted
unchanged, so the encoding is invisible to the common case.

This is a wire-format change, and the resulting skew behavior is deliberate:

- A Forge sender and a peer that does not unescape agree on every value that
  was already transmissible, because those values are not rewritten. A value
  that needed escaping arrives percent-escaped, where previously the call
  failed outright.
- A Forge receiver and a peer that does not escape agree on every value,
  including one containing a bare `%`: a value that fails to unescape is
  passed through unchanged rather than rejected.

Both directions therefore degrade to the previous behavior instead of
corrupting a value or failing a request. `Server`, `Client`, and `ServerStream`
all participate; the streaming server path is included, which the corresponding
upstream proposal does not cover.

## Errors

An error carries a transport-neutral `Kind` rather than an HTTP status code.
Each transport projects a `Kind` one way onto its own vocabulary, so no error
round-trips through a foreign code space. Under the previous design that round
trip was lossy: an error constructed as 422 arrived as 500 after a single gRPC
hop, because HTTP has more than sixty status codes and gRPC has seventeen.

Errors that form a service contract are declared in Protobuf with `kind`, and
`protoc-gen-go-errors` emits one immutable sentinel value per reason instead of
a constructor and a predicate. Matching therefore uses `errors.Is` rather than a
generated `IsXxx`, and `errors.KindOf` replaces `errors.Code`. The generator
rejects at build time what would otherwise become a wire-format inconsistency: a
reason that is not `SCREAMING_SNAKE_CASE`, one not prefixed by its enum name, or
a kind declared on the zero value.

Identity travels as `errdetails.ErrorInfo`, including the domain, so a caller
may match a remote error against the same sentinel it would use locally.
Aggregate failures travel as `errdetails.BadRequest`; `errors.Join` is not an
aggregate in this contract and drops all but the first error at the boundary.

The cause chain does not cross a process boundary. `errors.Unwrap` returns nil
on a received error and `errors.As` will not reach a remote type. Correlate
across services by trace ID instead: the tracing middleware stamps the ambient
trace onto every outgoing error, and a trace backend holds the fuller record.
Forge does not also serialize a cause summary onto the wire.

The `transport/http/status` package is removed. Its bidirectional code
conversion was the lossy step `Kind` exists to replace, and nothing referenced
it once each transport projected a `Kind` directly.

An error response is always `application/problem+json` and does not take part
in content negotiation. While it did, the same value was spelled two ways —
`NOT_FOUND` as JSON and `KIND_NOT_FOUND` as ProtoJSON — and a client reading the
shape it did not expect silently lost the kind or the reason. Negotiating a
result is useful; negotiating a failure only creates ways for two peers to
disagree. SSE and WebSocket error frames use the same document.

A kind the receiver does not recognize keeps its identity, and only its
classification falls back to the status line, so a peer running a newer version
stays understandable.

What crosses a boundary is `errors.Public`: the kind, the identity, and the
message, metadata, and violations the caller explicitly declared. A cause is
excluded structurally rather than by rule, so no configuration can leak one.

This replaces `errors.Policy` and its `PolicySafe`, `PolicyStrict`, and
`PolicyVerbose` values, all removed. A policy read the Kind and inferred
provenance it could not observe: it never inspected metadata or violation text,
and the three values were writable package variables any dependency could
reassign. Declaring what is public is something only the caller who wrote the
field can do, and `Msg`, `Meta`, `WithMetadata`, and `Violations` are how they
say so. An error from outside Forge discloses only `KindUnknown`; its text was
written for an operator.

A client reads an error body only when it is `application/problem+json`, is at
most `MaxProblemBytes` (64 KiB), and names a kind consistent with the response
status. A body contradicting the status is discarded in favour of the status
line, because a stale intermediary can serve an old body under a new status —
believing it let a caller match a 503 against a NotFound sentinel and stop
retrying. An unrecognized kind is not a contradiction: its identity is kept and
only the classification falls back to the status.

An identity is retained only as a complete domain and reason pair; half of one
cannot match a sentinel and is treated as anonymous.

`errors.IsRetryable` and `errors.Kind.Retryable` do not exist. Whether a retry
is safe depends on the class of failure, on whether the request reached a
server, and on whether the operation is idempotent; a Kind answers only the
first. `KindUnavailable` in particular covers both a connection that never
opened and a server that executed a request before its reply was lost, so a
boolean derived from the Kind alone would authorize repeating work that already
happened. Callers combine `errors.KindOf` with the idempotence declaration and
the transport's delivery evidence, or delegate to `middleware/retry`.

See [`docs/design/errors.md`](docs/design/errors.md).

## Client Retry Requires Evidence or a Declaration

`middleware/retry` retries a failed call only when the transport proved the
request never reached a server, or when the caller declared the operation
idempotent with `retry.Idempotent(ctx)`. An error offering neither is returned
after one attempt.

`KindUnavailable` is therefore not retried on its own. A service can report
itself unavailable after executing a request and before its reply arrives, so
repeating that call on a non-idempotent operation duplicates work that already
happened — the failure mode being avoided is a second charge or a second write,
which is among the hardest to trace back to its cause. Under the declaration,
`KindUnavailable` and `KindDeadlineExceeded` are both retried;
`KindResourceExhausted` and `KindConflict` are not, since retrying a
rate-limited call without server guidance worsens overload and a conflict needs
the caller to re-read state first.

Delivery evidence comes from the transport, which is the only layer that knows
whether bytes left the process. `transport.MarkNotSent` records the proof and
`transport.WasNotSent` reads it. The HTTP client marks node selection failures,
dial failures, and failed WebSocket handshakes. The gRPC client marks nothing:
grpc-go reports a failed dial and a connection lost mid-call as the same
`codes.Unavailable` status with no typed cause, so gRPC calls that want
automatic retries declare idempotence.

Restoring automatic retries for an operation is one call at the site that knows
the operation is safe to repeat, typically a generated wrapper or a thin client
facade.

See [`docs/design/retry.md`](docs/design/retry.md).

## Load Balancing Is Per Client

`selector.GlobalSelector` and `selector.SetGlobalSelector` are removed. A client
selects its policy with `http.WithSelector` or `grpc.WithSelector`, defaulting to
weighted round robin.

The global was reassignable by any dependency, read concurrently without
synchronization, and its default came from whichever transport happened to be
linked first. A per-client option removes all three: one dependency can no
longer change another client's balancing, and the default is a constant.

On gRPC the option applies to endpoints reached through discovery. A client
dialling a fixed address has a single node and consults no policy.

## HTTP Client Streams Declare What They Can Do

`ClientStream` carries what every stream can honour — `Header`, `Trailer`,
`CloseSend`, `Context`, `RecvMsg`. Sending is a separate capability:

```go
type SendingClientStream interface {
	ClientStream
	SendMsg(any) error
	CloseAndRecv(any) error
}
```

Server-sent events are one-directional, and the previous interface made that a
runtime error string on three methods it could not honour. A caller now learns
the same fact from the type: `Client.WebSocket` returns `SendingClientStream`,
`Client.ServerSentEvent` returns `ClientStream`, and code holding the narrower
one type-asserts to send.

`Send` and `Recv` are removed; they were aliases of `SendMsg` and `RecvMsg`.
Generated clients keep their existing method set, so service code calling them
needs no edit — but regeneration with the matching `protoc-gen-go-http` is
required.

## Protobuf Is Optional

`transport/http` speaks bytes and Go values. Everything needing a schema — HTTP
transcoding, ProtoJSON projection, path and query binding onto declared fields,
raw `HttpBody`, stream body fields — lives in `transport/http/transcoding`,
which installs itself into the transport when imported.

Generated bindings import it, so a service built from `.proto` files behaves
exactly as before and pays exactly as before. A service that serves plain JSON
imports neither it nor the Protobuf runtime:

| Application | Binary | Protobuf packages | gRPC packages |
| --- | --- | --- | --- |
| Plain JSON over `transport/http` | 11 MB | 0 | 0 |
| A library using only `errors` | 2.6 MB | 0 | 0 |
| Generated bindings and gRPC | 18 MB | 40 | 75 |

A hand-written service that binds Protobuf messages without generated code must
import the subpackage itself:

```go
import _ "github.com/sylphylabs/forge/transport/http/transcoding"
```

Without it, a raw `HttpBody` is not recognized, a path variable is not bound
onto a message field, and a stream body field reports that the schema runtime is
missing. The transport says so rather than failing silently.

`transport` no longer blank-imports the Protobuf codecs; `transcoding`
registers them. A service that needs `proto` or `protojson` content types
without generated code imports the subpackage for the same reason.

## Inherited Kratos v3 Behavior

The following are important Kratos v3 behaviors but are not Forge
differences:

- Logging uses `log/slog`.
- `encoding/json` and `encoding/protojson` are separate codecs.
- Generated HTTP handlers bind request data before service middleware runs.

These items only move into the difference sections after Forge changes
their behavior and completes validation.

## Migration

Follow [`docs/migration/kratos-to-forge.md`](docs/migration/kratos-to-forge.md)
for an executable migration checklist. Existing Kratos v2 applications should
first account for the Kratos v2-to-v3 API changes because Forge starts from
the v3 design rather than providing a direct v2 compatibility layer.

## Maintenance Rules

Every change that alters a public API, default behavior, wire format, module,
tool, or supported Go version must update this document in the same change.

Forge compatibility is not a reason by itself to retain an inferior public
API. A breaking replacement is acceptable only when its rationale, replacement
API, old and new code examples, regeneration requirements, and validation steps
are documented in the migration guide before the implementation is merged.

- Record current behavior here, not aspirations.
- Put pending upstream decisions in `docs/upstream-adoptions.md`.
- Put reproducible measurements in `docs/design/performance.md` or
  `docs/benchmarks/`.
- Add migration instructions and a replacement API before merging a breaking
  change; do not leave a compatibility shim without an explicit purpose.
- Link the implementation commit and focused tests after the change is
  committed.
- Re-verify the comparison date and baseline before each release.
