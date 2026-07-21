Translations: [English](README.md) | [简体中文](README_zh.md)

# OpenKratos

OpenKratos is an independent, pre-release fork of [go-kratos/kratos](https://github.com/go-kratos/kratos). It explores deliberate breaking changes, faster standard-library-based internals, and a smaller long-term dependency surface. It is not affiliated with or endorsed by the go-kratos maintainers.

The project currently tracks `v0` development and does not provide a stable API or compatibility guarantee. See [UPSTREAM.md](UPSTREAM.md) for its source baseline and synchronization policy.

## Features

- API-first development with Protobuf and generated HTTP/gRPC code.
- Unified [transport](https://go-kratos.dev/docs/component/transport/overview) layer for [HTTP](https://go-kratos.dev/docs/component/transport/http) and [gRPC](https://go-kratos.dev/docs/component/transport/grpc).
- Standard-library `http.ServeMux` routing with method patterns, path values, and Google AIP template support.
- Composable [middleware](https://go-kratos.dev/docs/component/middleware/overview) for recovery, logging, validation, tracing, metrics, auth, and more.
- Pluggable [registry](https://go-kratos.dev/docs/component/registry), [configuration](https://go-kratos.dev/docs/component/config), and [encoding](https://go-kratos.dev/docs/component/encoding) components.
- Standard-library `log/slog` based logging with OpenTelemetry extensions in contrib packages.
- Consistent metadata, errors, validation, OpenAPI, and code-generation workflows.
- A contrib ecosystem for optional integrations such as registries, config stores, middleware, encodings, and observability.

## Installation

### Requirements

- [Go](https://go.dev/dl/) 1.26 or later; Go 1.27 RC is used for forward validation
- [protoc](https://github.com/protocolbuffers/protobuf)
- [protoc-gen-go](https://github.com/protocolbuffers/protobuf-go)

### Install the CLI

```shell
go install github.com/openkratos/kratos/cmd/kratos@latest
kratos upgrade
```

## Create a Service

```shell
kratos new helloworld
cd helloworld
go mod tidy
kratos run
```

Visit `http://localhost:8000/helloworld/kratos` after the service starts.

For a fuller generated service flow:

```shell
kratos proto add api/helloworld/helloworld.proto
kratos proto client api/helloworld/helloworld.proto
kratos proto server api/helloworld/helloworld.proto -t internal/service
go generate ./...
kratos run
```

## Usage Example

```go
package main

import (
	"github.com/openkratos/kratos"
	"github.com/openkratos/kratos/transport/grpc"
	"github.com/openkratos/kratos/transport/http"
)

func main() {
	httpSrv := http.NewServer(http.Address(":8000"))
	grpcSrv := grpc.NewServer(grpc.Address(":9000"))

	app := kratos.New(
		kratos.Name("helloworld"),
		kratos.Version("v1.0.0"),
		kratos.Server(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

## Upstream Baseline

OpenKratos started from Kratos v3. Existing Kratos users should treat the module-path change and all future OpenKratos releases as an explicit migration, not as an in-place Kratos upgrade.

## Further Reading

- [Documentation](https://go-kratos.dev/docs/getting-started/start)
- [Examples](https://github.com/go-kratos/examples)
- [Project Layout](https://github.com/go-kratos/kratos-layout)
- [Upstream baseline and synchronization policy](UPSTREAM.md)
- [Performance modernization](docs/design/performance.md)
- [Upstream adoption ledger](docs/upstream-adoptions.md)
- [v2 to v3 upstream migration guide](docs/migration/v2-to-v3.md)
- [Community Contribution Guide](https://go-kratos.dev/docs/community/contribution)

## Development

```shell
make test
make lint
```

See [DEVELOPMENT.md](DEVELOPMENT.md) for multi-module checks and Go 1.27 RC validation.

## Security

Use a private GitHub security advisory in the OpenKratos repository. Do not report OpenKratos-specific vulnerabilities to the upstream Kratos project.

## Acknowledgments

OpenKratos preserves the complete Kratos Git history and original MIT copyright notice. The upstream Kratos project and its contributors created the foundation of this codebase.

The following projects influenced the original Kratos design:

- [go-kit/kit](https://github.com/go-kit/kit)
- [go-micro](https://github.com/asim/go-micro)
- [google/go-cloud](https://github.com/google/go-cloud)
- [go-zero](https://github.com/zeromicro/go-zero)
- [beego](https://github.com/beego/beego)

## License

OpenKratos is open-sourced software licensed under the [MIT license](./LICENSE).
