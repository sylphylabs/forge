# Development

## Toolchains

The repository currently builds with Go 1.26 and validates forward compatibility with Go 1.27 RC.

Install the current release candidate without replacing the system Go installation:

```shell
go install golang.org/dl/go1.27rc2@latest
go1.27rc2 download
go1.27rc2 version
```

## Modules

OpenKratos contains 28 Go modules. The root `go test ./...` command does not cover nested modules. Use the repository helpers for complete checks:

```shell
./hack/tools.sh tidy
./hack/tools.sh test
make lint
```

For a faster, non-race local pass across every module:

```shell
for mod in $(find . -name go.mod -exec dirname {} \; | sort); do
  (cd "$mod" && go test ./...)
done
```

Go 1.27 switches `x/net/http2` to its standard-library wrapper by default. OpenKratos requires `golang.org/x/net` v0.55.0 or newer because earlier wrapper releases omitted APIs used by gRPC-Go. Validate the native Go 1.27 path without compatibility build tags:

```shell
go1.27rc2 test ./...
```

## HTTP Routing

OpenKratos uses the Go standard library's `http.ServeMux` method and path tree. The transport compiles Google AIP path templates into standard-library patterns and restores the original variables through both `Context.Vars()` and `Request.PathValue()`.

This is intentionally not route-compatible with Gorilla mux:

- Literal and more-specific patterns win according to `http.ServeMux` precedence, independent of registration order.
- Conflicting patterns panic during registration instead of selecting the first registered route.
- AIP variables, terminal `**` wildcards, terminal custom verbs, and single-segment legacy regular expressions are supported.
- Arbitrary Gorilla regular expressions spanning multiple path segments are rejected. Use an AIP template instead.
- `StrictSlash` is deprecated and has no effect; path cleaning and trailing-slash redirects use `http.ServeMux` behavior.
- `HandlePrefix` uses path-segment prefix semantics rather than Gorilla's arbitrary string-prefix matching.
- Unmatched requests no longer fall through to the process-wide `http.DefaultServeMux`. Pass it explicitly to `NotFoundHandler` if that behavior is required.

Run the router scaling benchmarks with:

```shell
go test ./transport/http -run '^$' -bench '^BenchmarkRouteMux$' -benchmem
```

## Repository Identity

All OpenKratos modules and generated Go package paths use the `github.com/openkratos/kratos` prefix. Internal cross-module requirements use the temporary `v0.0.0` version together with local `replace` directives until the first OpenKratos tags are published.

The `upstream` remote cannot be pushed to from this checkout. Add the OpenKratos repository as `origin` only after it has been created:

```shell
git remote add origin git@github.com:openkratos/kratos.git
```

Do not push upstream Kratos tags to `origin`.
