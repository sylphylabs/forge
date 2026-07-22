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

## 6. Review Streaming Timeouts

HTTP SSE and WebSocket streams are not terminated by the unary server timeout.
Add explicit read, write, idle, or application lifetime policies where the
service requires them.

## 7. Keep Inherited v3 Migrations

OpenKratos retains the Kratos v3 `log/slog` logging model, standard-compatible
errors, and the separate `json` and `protojson` codecs. A service already on
Kratos v3 should not undo those migrations.

The current HTTP generator still does not support protobuf Editions or the
Opaque API. Keep schemas on the supported protobuf API until that work is
recorded as implemented in `COMPATIBILITY.md`.

## 8. Validate the Migration

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
- [ ] Define explicit HTTP stream lifetime policies.
- [ ] Run race tests, vet, and service integration tests.
- [ ] Review `COMPATIBILITY.md` again before release.
