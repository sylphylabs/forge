# Migrating from Kratos v3 to OpenKratos

OpenKratos is a pre-release fork, not an in-place Kratos upgrade. Perform the
migration on a branch and review the current differences in
[`COMPATIBILITY.md`](../../COMPATIBILITY.md) before changing dependencies.
OpenKratos may replace Kratos APIs instead of retaining compatibility shims;
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

OpenKratos requires Go 1.27. Upgrade the project toolchain before changing the
module path.

## 2. Replace Module Paths

Replace the root import prefix:

```text
github.com/go-kratos/kratos/v3
github.com/openkratos/kratos
```

Replace each used contrib module separately. OpenKratos is on v0, so remove the
upstream `/v3` suffix:

```text
github.com/go-kratos/kratos/contrib/middleware/jwt/v3
github.com/openkratos/kratos/contrib/middleware/jwt
```

During pre-release development, resolve the root module from `main`:

```shell
go get github.com/openkratos/kratos@main
go mod tidy
```

Use released versions rather than `main` after OpenKratos publishes tags.

Update provider SDK imports when constructing contrib adapters directly:

```text
github.com/hashicorp/consul/api
github.com/hashicorp/consul/api/v2

github.com/nacos-group/nacos-sdk-go/clients/config_client
github.com/nacos-group/nacos-sdk-go/v2/clients/config_client
```

Apollo integrations use `github.com/apolloconfig/agollo/v5`. OpenKratos uses
the Go 1.27 standard-library `uuid` package rather than Google or gofrs UUID
types. The default application ID is now UUIDv4 rather than UUIDv1; configure an
explicit ID if its value or version is part of an operational contract.

## 3. Update Code Generators

OpenKratos owns its public error descriptor in the separate
`github.com/openkratos/api` module. The runtime still emits and accepts the
four-field `{code, reason, message, metadata}` error envelope; it does not use
`google.rpc.Status` as a replacement.

Most applications construct errors through `errors.New`, `errors.Newf`, or the
generated error helpers and need no runtime code change. Code that explicitly
names the old generated message must update its import and type:

```text
github.com/go-kratos/kratos/v3/errors.Status
github.com/openkratos/api/errors/v1.Status
```

The API import path `github.com/openkratos/api/errors/v1` declares package
`errors`. The runtime aliases that package as `errorapi` and embeds
`errorapi.Status` in `errors.Error`, so field selectors such as `err.Code`,
`err.Reason`, `err.Message`, and `err.Metadata` remain unchanged. Update
`.proto` imports and enum annotations when the OpenKratos API module is
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

// OpenKratos
import "openkratos/errors/v1/errors.proto";
enum ErrorReason {
  option (openkratos.errors.v1.default_code) = 500;
  ERROR_REASON_UNSPECIFIED = 0;
}
```

Enum-value overrides similarly change from `(errors.code)` to
`(openkratos.errors.v1.code)`. Regenerate helpers after changing the source;
the generator no longer carries a private fallback descriptor for the old
annotations.

Replace the inherited generator modules with the three atomic OpenKratos
commands. During local development they share one source module:

```text
github.com/openkratos/kratos/cmd
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

# OpenKratos local cutover
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
`_middleware.pb.go`; delete obsolete `_openkratos.pb.go` files before checking
the regenerated diff. No forwarding `protoc-gen-go-openkratos` command or
`--go-openkratos_out` flag is retained.

Published projects will replace these local entries with pinned
`buf.build/openkratos/go-errors`, `go-http`, and `go-middleware` revisions after
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

OpenKratos does not provide the general `kratos` executable.

| Previous workflow | OpenKratos workflow |
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

OpenKratos uses standard-library `http.ServeMux` precedence instead of Gorilla
mux registration order. Review every hand-written route and add tests for:

- overlapping literal and variable patterns;
- conflicts that now panic during registration;
- trailing slashes and path cleaning;
- custom regular expressions;
- prefix handlers;
- expected 404 and 405 responses.

Replace multi-segment Gorilla regular expressions with Google AIP templates.
Remove `http.StrictSlash(...)` from server construction; OpenKratos uses
`http.ServeMux` path cleaning and trailing-slash behavior. If the service
intentionally used `http.DefaultServeMux` as a fallback, pass it explicitly
through `NotFoundHandler`.

## 6. Migrate Server Middleware

OpenKratos removes the server-side selector API instead of retaining a runtime
compatibility path. Apply these mechanical renames first:

| Kratos name | OpenKratos name |
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

Regenerate every HTTP client and server. OpenKratos validates inline unary
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

OpenKratos retains the Kratos v3 `log/slog` logging model, standard-compatible
errors, and the separate `json` and `protojson` codecs. A service already on
Kratos v3 should not undo those migrations.

The HTTP generator supports Edition 2023 Open and Opaque APIs. Regenerate from
the schema rather than retaining code produced by the upstream generator.

## 10. Validate the Migration

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
- [ ] Pin the OpenKratos generator revisions.
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
- [ ] Run race tests, vet, and service integration tests.
- [ ] Review `COMPATIBILITY.md` again before release.
