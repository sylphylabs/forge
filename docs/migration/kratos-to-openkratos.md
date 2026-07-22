# Migrating from Kratos v3 to OpenKratos

OpenKratos is a pre-release fork, not an in-place Kratos upgrade. Perform the
migration on a branch and review the current differences in
[`COMPATIBILITY.md`](../../COMPATIBILITY.md) before changing dependencies.

## 1. Establish a Baseline

Before changing imports:

```shell
go test ./...
go vet ./...
```

Commit or otherwise preserve generated code and `go.mod` so the migration diff
can be reviewed independently from unrelated changes.

OpenKratos requires Go 1.26. Upgrade the project toolchain before changing the
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

## 3. Update Code Generators

Change generator module paths and pin the selected revision in the project's
Buf configuration or tool dependencies:

```text
github.com/openkratos/kratos/cmd/protoc-gen-go-http
github.com/openkratos/kratos/cmd/protoc-gen-go-errors
```

Regenerate HTTP clients, HTTP servers, and error helpers after changing the
module path:

```shell
buf generate
go generate ./...
go mod tidy
```

Do not edit generated `.pb.go` files with global text replacement. Change the
`.proto` source and generator configuration, then regenerate.

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
`StrictSlash` no longer changes behavior. If the service intentionally used
`http.DefaultServeMux` as a fallback, pass it explicitly through
`NotFoundHandler`.

## 6. Review Google HTTP Transcoding

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

Hand-written callers of `transport/http.BuildPath` must handle its error:

```go
path, err := http.BuildPath(pattern, request, http.WithQueryParams())
if err != nil {
	return err
}
```

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

## 7. Review Streaming Timeouts

HTTP SSE and WebSocket streams are not terminated by the unary server timeout.
Add explicit read, write, idle, or application lifetime policies where the
service requires them.

## 8. Keep Inherited v3 Migrations

OpenKratos retains the Kratos v3 `log/slog` logging model, standard-compatible
errors, and the separate `json` and `protojson` codecs. A service already on
Kratos v3 should not undo those migrations.

The HTTP generator supports Edition 2023 Open and Opaque APIs. Regenerate from
the schema rather than retaining code produced by the upstream generator.

## 9. Validate the Migration

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

- [ ] Upgrade to Go 1.26 or later.
- [ ] Replace the root Kratos v3 module path.
- [ ] Replace every used contrib module path and remove `/v3`.
- [ ] Pin the OpenKratos generator revisions.
- [ ] Regenerate all generated Go files from source.
- [ ] Replace `kratos` CLI commands with Go and Buf commands.
- [ ] Review route precedence, conflicts, prefixes, slashes, 404, and 405.
- [ ] Regenerate and test every inline `google.api.HttpRule` binding.
- [ ] Migrate `BuildPath` callers to handle errors.
- [ ] Review body/query classification, ProtoJSON wire values, and `%2F` paths.
- [ ] Define explicit HTTP stream lifetime policies.
- [ ] Run race tests, vet, and service integration tests.
- [ ] Review `COMPATIBILITY.md` again before release.
