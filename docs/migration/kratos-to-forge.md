# Migrating from Kratos v3 to Forge

Forge is a pre-release fork, not an in-place Kratos upgrade. Perform the
migration on a branch and review the current differences in
[`COMPATIBILITY.md`](../../COMPATIBILITY.md) before changing dependencies.
Forge may replace Kratos APIs instead of retaining compatibility shims;
each accepted removal is documented here with its replacement and validation
steps.

## 1. Establish a Baseline

Before changing imports:

```shell
go test ./...
go vet ./...
```

Commit or otherwise preserve generated code and `go.mod` so the migration diff
can be reviewed independently from unrelated changes.

Forge requires Go 1.27. Upgrade the project toolchain before changing the
module path.

## 2. Replace Module Paths

Replace the root import prefix:

```text
github.com/go-kratos/kratos/v3
github.com/sylphylabs/forge
```

Replace each used contrib module separately. Forge is on v0, so remove the
upstream `/v3` suffix:

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/sylphylabs/forge/contrib/middleware/jwt
```

During pre-release development, resolve the root module from `main`:

```shell
go get github.com/sylphylabs/forge@main
go mod tidy
```

Use released versions rather than `main` after Forge publishes tags.

Update provider SDK imports when constructing contrib adapters directly:

```text
github.com/hashicorp/consul/api
github.com/hashicorp/consul/api/v2

github.com/nacos-group/nacos-sdk-go/clients/config_client
github.com/nacos-group/nacos-sdk-go/v2/clients/config_client
```

Apollo integrations use `github.com/apolloconfig/agollo/v5`. Forge uses
the Go 1.27 standard-library `uuid` package rather than Google or gofrs UUID
types. The default application ID is now UUIDv4 rather than UUIDv1; configure an
explicit ID if its value or version is part of an operational contract.

## 3. Update Code Generators

Forge owns its public error descriptor in the separate
`github.com/sylphylabs/forge/api` module. The runtime still emits and accepts the
four-field `{code, reason, message, metadata}` error envelope; it does not use
`google.rpc.Status` as a replacement.

Most applications construct errors through `errors.New`, `errors.Newf`, or the
generated error helpers and need no runtime code change. Code that explicitly
names the old generated message must update its import and type:

```text
github.com/go-kratos/kratos/v3/errors.Status
github.com/sylphylabs/forge/api/errors/v1.Status
```

The API import path `github.com/sylphylabs/forge/api/errors/v1` declares package
`errors`. The runtime aliases that package as `errorapi` and embeds
`errorapi.Status` in `errors.Error`, so field selectors such as `err.Code`,
`err.Reason`, `err.Message`, and `err.Metadata` remain unchanged. Update
`.proto` imports and enum annotations when the Forge API module is
published; do not retain or vendor one of the inherited `errors/errors.proto`
copies.

The schema rewrite is:

```proto
// Before
import "errors/errors.proto";
enum ErrorReason {
  option (errors.default_code) = 500;
  ERROR_REASON_UNSPECIFIED = 0;
}

// Forge
import "sylphy/errors/v1/errors.proto";
enum ErrorReason {
  option (sylphy.errors.v1.default_code) = 500;
  ERROR_REASON_UNSPECIFIED = 0;
}
```

Enum-value overrides similarly change from `(errors.code)` to
`(sylphy.errors.v1.code)`. Regenerate helpers after changing the source;
the generator no longer carries a private fallback descriptor for the old
annotations.

Replace the inherited generator modules with the three atomic Forge
commands. During local development they share one source module:

```text
github.com/sylphylabs/forge/cmd
```

Keep errors and HTTP generation independently selectable, and add middleware
generation only when the application uses generated service plans:

```yaml
# Before
plugins:
  - local: protoc-gen-go-http
    out: gen/go
    opt: paths=source_relative,omitempty=true
  - local: protoc-gen-go-errors
    out: gen/go
    opt: paths=source_relative

# Forge local cutover
plugins:
  - local: protoc-gen-go-errors
    out: gen/go
    opt: paths=source_relative
  - local: protoc-gen-go-http
    out: gen/go
    opt: paths=source_relative,omitempty=true
  - local: protoc-gen-go-middleware
    out: gen/go
    opt: paths=source_relative,http=annotated,grpc=true
```

The HTTP plugin retains its option names. The middleware HTTP method-set option
must match the HTTP binding policy:

| `go-http` | matching `go-middleware` |
| --- | --- |
| `omitempty=true` | `http=annotated` |
| `omitempty=false` | `http=all` |

Omit the middleware `http` option when no HTTP wrapper is required. Set
`grpc=true` only when the same generation pipeline runs
`protoc-gen-go-grpc`. The three outputs are `_errors.pb.go`, `_http.pb.go`, and
`_middleware.pb.go`; delete obsolete `_forge.pb.go` files before checking
the regenerated diff. No forwarding `protoc-gen-go-forge` command or
`--go-forge_out` flag is retained.

Published projects will replace these local entries with pinned
`buf.build/forge/go-errors`, `go-http`, and `go-middleware` revisions after
those plugins are public. Do not use an unpinned development revision.

```shell
buf generate
go generate ./...
go mod tidy
```

Do not edit generated `.pb.go` files with global text replacement. Change the
`.proto` source and generator configuration, then regenerate.

Current generated HTTP files assert
`transport/http.SupportPackageIsVersion5`. This intentionally catches a stale
runtime paired with a newer generator at compile time. Version 3 and 4
sentinels are no longer exported. Regenerate clients and servers before
upgrading the runtime; upgrading only the runtime module does not rewrite
generated code.

## 4. Replace Kratos CLI Workflows

Forge does not provide the general `kratos` executable.

| Previous workflow | Forge workflow |
| --- | --- |
| `kratos new` | Create a normal Go module or use a reviewed repository template |
| `kratos run` | Run the service with `go run` |
| `kratos proto ...` | Run the repository's Buf or `protoc` pipeline |
| `kratos upgrade` | Use the Go module toolchain |

For the conventional Kratos layout, the direct run command is typically:

```shell
go run ./cmd/server -conf ./configs
```

## 5. Review HTTP Routes

Forge uses standard-library `http.ServeMux` precedence instead of Gorilla
mux registration order. Review every hand-written route and add tests for:

- overlapping literal and variable patterns;
- conflicts that now panic during registration;
- trailing slashes and path cleaning;
- custom regular expressions;
- prefix handlers;
- expected 404 and 405 responses.

Replace multi-segment Gorilla regular expressions with Google AIP templates.
Remove `http.StrictSlash(...)` from server construction; Forge uses
`http.ServeMux` path cleaning and trailing-slash behavior. If the service
intentionally used `http.DefaultServeMux` as a fallback, pass it explicitly
through `NotFoundHandler`.

## 6. Migrate Server Middleware

Forge removes the server-side selector API instead of retaining a runtime
compatibility path. Apply these mechanical renames first:

| Kratos name | Forge name |
| --- | --- |
| `middleware.Handler` | `middleware.UnaryHandler` |
| `middleware.Middleware` | `middleware.UnaryMiddleware` |
| `middleware.Chain` | `middleware.ChainUnary` |

Remove `http.Middleware`, `grpc.Middleware`, `grpc.StreamMiddleware`,
`Server.Use`, `Server.WrapMiddleware`, and `http.Context.Middleware`. Resolve
each selector to its exact protobuf methods and assign the middleware directly
to the generated service plan:

```go
plan := pb.GreeterMiddleware{
	Unary: []middleware.UnaryMiddleware{
		recovery.Recovery(),
		logging.Server(logger),
	},
	Methods: pb.GreeterMethodMiddleware{
		SayHello: []middleware.UnaryMiddleware{authorizeSayHello},
	},
}

httpService, err := pb.WrapGreeterHTTPServer(service, plan)
if err != nil {
	return err
}
pb.RegisterGreeterHTTPServer(httpServer, httpService)

grpcService, err := pb.WrapGreeterGRPCServer(service, plan)
if err != nil {
	return err
}
pb.RegisterGreeterServer(grpcServer, grpcService)
```

`Unary` and `Stream` apply at service scope; fields under `Methods` append
method-specific middleware. The first item is outermost. Wrapper construction
rejects nil middleware or nil returned handlers and snapshots every slice, so
later plan mutation has no effect.

Convert whole-stream behavior to `middleware.StreamMiddleware`. It runs once
around the stream lifecycle; decorate `middleware.ServerStream` when behavior
must observe each `SendMsg` or `RecvMsg`. The old gRPC stream middleware path
constructed a handler chain without invoking it, so migrated lifecycle
middleware now actually runs. Treat that as a correctness fix when comparing
behavior.

Raw HTTP request/response behavior belongs in `http.Filter`. Native gRPC
metadata, peer, status, compression, header, or trailer behavior belongs in
`grpc.UnaryInterceptor`, `grpc.StreamInterceptor`, or `grpc.Options`. These
transport-native layers run outside generated service middleware.

`middleware/selector.Server` is removed with the server selector path.
`middleware/selector.Client` remains available for client-side operation
selection.

## 7. Review Google HTTP Transcoding

Regenerate every HTTP client and server. Forge validates inline unary
`google.api.HttpRule` declarations more strictly and may reject schemas that the
previous generator accepted with a warning or deferred until runtime.

- Remove `response_body: "*"`; omission means the whole response.
- Keep `body` and `response_body` on top-level fields.
- Remove map and repeated-message fields from query position. Bind them to the
  body, or redesign the request.
- Remove nested `additional_bindings` and duplicate/conflicting route match
  sets.
- Expect generated Google JSON endpoints to use `application/json` with
  protobuf JSON wire representations, including quoted 64-bit integers and
  standard base64 bytes.
- Expect the generated client to use only the primary binding. Additional
  bindings remain raw REST entry points on the server.

Generated clients compile each fixed binding once and reuse a concurrency-safe
`CompiledPath`; expansion errors are still returned before network I/O.

Hand-written callers of `transport/http.BuildPath` must handle its error. Keep
this API when the template itself is selected dynamically:

```go
path, err := http.BuildPath(pattern, request, http.WithQueryParams())
if err != nil {
	return err
}
```

For a fixed template used repeatedly, replace per-request compilation:

```go
// Before: parses and validates the same template on every call.
path, err := http.BuildPath(
	"/v1/users/{name}", request, http.WithQueryParams(),
)
```

with a reusable path plan:

```go
var userPath = http.MustCompilePath(
	"/v1/users/{name}", new(pb.GetUserRequest), http.WithQueryParams(),
)

path, err := userPath.Build(request)
```

Use `CompilePath` in constructors or setup code when invalid configuration
should be returned as an error rather than panic. Use `MustCompilePath` for
literal templates owned by the program or generated code.

A generated client cannot infer a method for primary `custom.kind: "*"`, or a
request value for a primary bare `*`/`**` path wildcard. Those calls now return
`ErrUnspecifiedHTTPMethod` or `ErrUnboundPathWildcard` before network I/O. Use a
concrete primary rule and keep ambiguous forms as additional server bindings
when a generated Go client is required.

Encoded slashes are security-sensitive. Add integration tests for resource-name
paths: multi-segment variables preserve `%2F`/`%2f`, while single-segment
variables fully decode them.

External `google.api.Service` YAML and `fully_decode_reserved_expansion` are not
yet supported.

## 8. Review Streaming Timeouts

HTTP SSE and WebSocket streams are not terminated by the unary server timeout.
Add explicit read, write, idle, or application lifetime policies where the
service requires them.

## 9. Keep Inherited v3 Migrations

Forge retains the Kratos v3 `log/slog` logging model, standard-compatible
errors, and the separate `json` and `protojson` codecs. A service already on
Kratos v3 should not undo those migrations.

The HTTP generator supports Edition 2023 Open and Opaque APIs. Regenerate from
the schema rather than retaining code produced by the upstream generator.

## 10. Replace Legacy Metrics Middleware

Forge removes the inherited generic metrics middleware without a
compatibility shim or a second metric stream. Instrument HTTP at its native
filter and `RoundTripper` boundaries:

```go
serverMetrics, err := metrics.NewHTTPServerFilter(provider)
if err != nil {
	return err
}
server := kratoshttp.NewServer(kratoshttp.Filter(serverMetrics))

clientMetrics, err := metrics.NewHTTPClientWrapper(provider)
if err != nil {
	return err
}
client, err := kratoshttp.NewClient(
	ctx,
	kratoshttp.WithEndpoint(endpoint),
	kratoshttp.WithRoundTripperWrapper(clientMetrics),
)
```

The MeterProvider is required. Pass `metric/noop.NewMeterProvider()` when an
explicitly disabled path is needed; the package does not read the global
provider or silently install a no-op provider.

Use grpc-go's gRFC A66 implementation directly for gRPC:

```go
metricSet := grpcstats.NewMetricSet(
	grpcotel.ClientCallDurationMetricName,
	grpcotel.ClientAttemptDurationMetricName,
	grpcotel.ServerCallDurationMetricName,
)
otelOptions := grpcotel.Options{
	MetricsOptions: grpcotel.MetricsOptions{
		MeterProvider: provider,
		Metrics:       metricSet,
	},
}

server := kratosgrpc.NewServer(
	kratosgrpc.Options(grpcotel.ServerOption(otelOptions)),
)
conn, err := kratosgrpc.NewClient(
	ctx,
	kratosgrpc.WithOptions(grpcotel.DialOption(otelOptions)),
)
```

Do not leave `Metrics` nil and do not use `DefaultMetrics()`. Either choice
enables started and message-size metrics and may adopt future grpc-go defaults.
Keep `TraceOptions` at its zero value when existing tracing is installed, or
the service can create duplicate spans.

Replace the old API as follows:

| Kratos API | Forge replacement |
| --- | --- |
| `metrics.Server(...)` | `metrics.NewHTTPServerFilter(provider, ...)` for HTTP; `grpcotel.ServerOption` for gRPC |
| `metrics.Client(...)` | `metrics.NewHTTPClientWrapper(provider, ...)` for HTTP; `grpcotel.DialOption` for gRPC |
| `Option`, `WithRequests`, `WithSeconds` | Removed; the transport-specific constructor owns its fixed instrument set |
| `DefaultRequestsCounter` | Use the duration histogram's count |
| `DefaultSecondsHistogram` and `Default*` names | Fixed HTTP semconv or gRPC A66 instruments |
| `DefaultSecondsHistogramView` | An application SDK View |
| `EnableOTELExemplar` | `sdkmetric.WithExemplarFilter(...)` or `OTEL_METRICS_EXEMPLAR_FILTER` in application configuration |

Metric streams change as follows. The old generic stream was split by
transport, so choose the row matching each old `kind` value:

| Legacy instrument | HTTP replacement | gRPC replacement |
| --- | --- | --- |
| `server_requests_seconds` | `http.server.request.duration` | `grpc.server.call.duration` |
| `client_requests_seconds` | `http.client.request.duration` | `grpc.client.call.duration` |
| `server_requests_code_total` | `http.server.request.duration` count | `grpc.server.call.duration` count |
| `client_requests_code_total` | `http.client.request.duration` count | `grpc.client.call.duration` count |

The legacy gRPC client middleware ran once per logical unary call. Migrate its
rates, error ratios, and latency SLOs to `grpc.client.call.duration`. The A66
`grpc.client.attempt.duration` stream is additional retry and hedging evidence:
one logical call can produce multiple attempts, so attempt count is not an
equivalent replacement. A66 also adds complete streaming lifecycle coverage
that the legacy unary middleware did not provide.

With standard Prometheus name translation, the duration streams are commonly
exported as:

| OTel instrument | Prometheus histogram base name |
| --- | --- |
| `http.server.request.duration` | `http_server_request_duration_seconds` |
| `http.client.request.duration` | `http_client_request_duration_seconds` |
| `grpc.server.call.duration` | `grpc_server_call_duration_seconds` |
| `grpc.client.call.duration` | `grpc_client_call_duration_seconds` |
| `grpc.client.attempt.duration` | `grpc_client_attempt_duration_seconds` |

Prometheus histograms expose the base name with `_bucket`, `_sum`, and `_count`
suffixes. Rewrite counter-based rates to the corresponding `_count` time
series, preserving only bounded semantic attributes. Exporter or Collector
translation settings can alter final names, so verify the deployed scrape
output before changing dashboards and alerts.

The old `kind`, `operation`, `code`, and `reason` labels do not carry forward as
a bundle. Use the standard protocol-specific method, route/target, status, and
`error.type` attributes. `reason` has no metric replacement; keep detailed
business failure reasons in traces and logs. Configure custom histogram buckets
with an SDK View and exemplar policy on the SDK MeterProvider.

HTTP timing boundaries also change. Server duration covers the complete
`ServeHTTP` call. Client duration ends when response headers arrive or the
transport fails, so it no longer includes response-body reads or the Kratos
decoder. Redirect attempts are independent `RoundTrip` measurements. Review
latency SLOs rather than comparing the new and old client series as equivalent.

The complete attribute, status, route, cardinality, and lifecycle contract is
in [`docs/design/otel-metrics.md`](../design/otel-metrics.md).

## 11. Validate the Migration

Run generation before tests so stale imports cannot hide in generated files:

```shell
buf generate
go mod tidy
go test -race ./...
go vet ./...
```

Exercise HTTP routing, streaming, discovery, configuration reloads, and
graceful shutdown in integration tests used by the service.

## Checklist

- [ ] Upgrade to Go 1.27 or later.
- [ ] Replace the root Kratos v3 module path.
- [ ] Replace every used contrib module path and remove `/v3`.
- [ ] Update Consul, Nacos, and Apollo provider SDK import paths.
- [ ] Replace direct Google/gofrs UUID imports and review the application ID version change.
- [ ] Pin the Forge generator revisions.
- [ ] Regenerate all generated Go files from source.
- [ ] Confirm generated HTTP files assert `SupportPackageIsVersion5`.
- [ ] Replace `kratos` CLI commands with Go and Buf commands.
- [ ] Review route precedence, conflicts, prefixes, slashes, 404, and 405.
- [ ] Rename unary middleware types and regenerate service middleware plans.
- [ ] Replace server selectors with generated method fields and wrappers.
- [ ] Convert stream lifecycle behavior to `StreamMiddleware` and decorate `ServerStream` for per-message behavior.
- [ ] Move transport-native behavior to HTTP filters or gRPC interceptors.
- [ ] Regenerate and test every inline `google.api.HttpRule` binding.
- [ ] Keep `BuildPath` for dynamic templates; compile repeated fixed templates once.
- [ ] Review body/query classification, ProtoJSON wire values, and `%2F` paths.
- [ ] Define explicit HTTP stream lifetime policies.
- [ ] Replace generic metrics middleware with the HTTP filter/wrapper and explicit gRPC A66 metric set.
- [ ] Rewrite counter queries to histogram `_count` and remove `reason`-based metric dimensions.
- [ ] Verify Prometheus names, buckets, exemplars, dashboards, and alerts against deployed exporter output.
- [ ] Run race tests, vet, and service integration tests.
- [ ] Review `COMPATIBILITY.md` again before release.
