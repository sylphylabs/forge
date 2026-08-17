Translations: [English](README.md) | [简体中文](README_zh.md)

# Forge

Forge is an independent, pre-release fork of [go-kratos/kratos](https://github.com/go-kratos/kratos). It explores deliberate breaking changes, faster standard-library-based internals, and a smaller long-term dependency surface. It is not affiliated with or endorsed by the go-kratos maintainers.

The project currently tracks `v0` development and does not provide a stable API or compatibility guarantee. Read [COMPATIBILITY.md](COMPATIBILITY.md) before migrating and [UPSTREAM.md](UPSTREAM.md) for the source baseline and synchronization policy.

## Features

- API-first development with Protobuf and generated HTTP/gRPC code.
- Unified transport layer for HTTP and gRPC.
- Protocol-neutral asynchronous message contract with optional broker adapters.
- Standard-library `http.ServeMux` routing with method patterns, path values, and Google AIP template support.
- Composable middleware for recovery, logging, validation, tracing, metrics, auth, and more.
- Pluggable registry, configuration, and encoding components.
- Standard-library `log/slog` based logging with OpenTelemetry extensions in contrib packages.
- Consistent metadata, errors, validation, OpenAPI, and code-generation workflows.
- A contrib ecosystem for optional integrations such as registries, config stores, middleware, encodings, and observability.

## Installation

### Requirements

- [Go](https://go.dev/dl/) 1.27 RC (currently 1.27rc3; `go.mod` requires it, so Go 1.26 cannot build Forge)
- [protoc](https://github.com/protocolbuffers/protobuf)
- [protoc-gen-go](https://github.com/protocolbuffers/protobuf-go)
- [Buf](https://buf.build/) or an equivalent `protoc` workflow

### Add Forge

```shell
go get github.com/sylphylabs/forge@main
```

Forge intentionally does not ship a project-scaffolding CLI. Project
creation, dependency upgrades, and execution use the standard Go toolchain.

Build the atomic Forge Protobuf generators from this checkout until the
corresponding Buf plugins are published. Install
`./protoc-gen-go-message` only when a service consumes asynchronous messages:

```shell
cd cmd
GOWORK=off go install ./protoc-gen-go-errors ./protoc-gen-go-http ./protoc-gen-go-message ./protoc-gen-go-middleware
```

## Generate and Run

Use the repository's Buf or `protoc` configuration to generate code, then run
the service directly:

```shell
buf generate
go generate ./...
go run ./cmd/server -conf ./configs
```

## Usage Example

```go
package main

import (
	"github.com/sylphylabs/forge"
	"github.com/sylphylabs/forge/transport/grpc"
	"github.com/sylphylabs/forge/transport/http"
)

func main() {
	httpSrv := http.NewServer(http.WithAddress(":8000"))
	grpcSrv := grpc.NewServer(grpc.WithAddress(":9000"))

	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithVersion("v1.0.0"),
		forge.WithServer(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

## Upstream Baseline

Forge started from Kratos v3. Existing Kratos users should treat the module-path change and all future Forge releases as an explicit migration, not as an in-place Forge upgrade.

## Further Reading

- [Usage guides](docs/README.md#usage-guides) — errors, observability, middleware, application
- [Guidance for coding agents](AGENTS.md)
- [Compatibility contract](COMPATIBILITY.md)
- [Migration from Kratos v3](docs/migration/kratos-to-forge.md)
- [Documentation index](docs/README.md)
- [Upstream baseline and synchronization policy](UPSTREAM.md)
- [Performance modernization](docs/design/performance.md)
- [Upstream adoption ledger](docs/upstream-adoptions.md)
- [Contribution guide](CONTRIBUTING.md)
- [Upstream Kratos documentation](https://go-kratos.dev/docs/getting-started/start) (reference only; behavior may differ)

## Development

```shell
make test
make lint
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for multi-module checks and Go 1.27 RC validation.

## Security

Use a private GitHub security advisory in the Forge repository. Do not report Forge-specific vulnerabilities to the upstream Kratos project.

## Acknowledgments

Forge preserves the complete Kratos Git history and original MIT copyright notice. The upstream Kratos project and its contributors created the foundation of this codebase.

The following projects influenced the original Forge design:

- [go-kit/kit](https://github.com/go-kit/kit)
- [go-micro](https://github.com/asim/go-micro)
- [google/go-cloud](https://github.com/google/go-cloud)
- [go-zero](https://github.com/zeromicro/go-zero)
- [beego](https://github.com/beego/beego)

## License

Forge is open-sourced software licensed under the [MIT license](./LICENSE).
