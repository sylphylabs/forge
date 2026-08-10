# Forge for coding agents

Forge is a Go microservice framework. This file is the entry point for an agent
working in this repository or writing an application against Forge. It states
the rules that are load-bearing and points at the topic guides; it does not
repeat their content.

Read the guide for the topic you are working on before writing code. The guides
are short, example-first, and describe the API as it exists today.

| Working on | Read |
| --- | --- |
| Defining, returning, or matching errors; declaring error enums in Protobuf | [docs/agent/errors.md](docs/agent/errors.md) |
| Tracing, metrics, or logs; wiring OpenTelemetry | [docs/agent/observability.md](docs/agent/observability.md) |
| Writing or attaching middleware | [docs/agent/middleware.md](docs/agent/middleware.md) |
| Starting a service, lifecycle hooks, transports, config | [docs/agent/application.md](docs/agent/application.md) |

## Do not write these

Forge began as a fork of [Kratos](https://github.com/go-kratos/kratos) and
diverged. The following are the APIs a model trained on Kratos reaches for that
do not exist here. Each one fails to compile.

| Do not write | Write instead |
| --- | --- |
| `grpc.NewServer(grpc.Middleware(m))` | Server middleware is generated. `WrapGreeterGRPCServer(srv, GreeterMiddleware{Unary: ...})` — see [middleware.md](docs/agent/middleware.md) |
| `http.NewServer(http.Middleware(m))` | Same: `WrapGreeterHTTPServer(srv, GreeterMiddleware{...})` |
| `middleware.Middleware` / `middleware.Handler` | `middleware.UnaryMiddleware` / `middleware.UnaryHandler`. Streams use `StreamMiddleware` / `StreamHandler` |
| `errors.New(404, "REASON", "msg")` | Errors carry a `Kind`, never a status code: `errors.New(errors.KindNotFound)`. See [errors.md](docs/agent/errors.md) |
| `errors.Newf` / `errors.Errorf` / `err.WithCause(c)` | `.Msgf(...)` and `.Wrap(cause)` |
| `errors.IsNotFound(err)` | `errors.Is(err, v1.ErrNotFound)` or `errors.KindOf(err) == errors.KindNotFound` |
| A `forge` / `kratos` CLI to scaffold a project | Forge ships no scaffolding CLI. Use the Go toolchain and `buf generate` |

There is no `Middleware` server option on either transport. Do not add one to
make an example work — check [middleware.md](docs/agent/middleware.md) instead.

## Repository shape

Forge is a multi-module repository. The root module, `api/`, and each directory
under `contrib/` have their own `go.mod`, wired to each other with local
`replace` directives.

- Run tests and builds **in the module you changed**: a `go test ./...` at the
  root does not compile `contrib/otel`.
- `make test` and `make lint` at the root cover the multi-module set. See
  [DEVELOPMENT.md](DEVELOPMENT.md).
- The root module must not gain an OpenTelemetry SDK dependency. Observability
  integrations live in `contrib/otel`, which is why they are a separate module.
- Generated Protobuf code is committed. Regenerate with `buf generate`; the
  generators are in `cmd/` (`protoc-gen-go-errors`, `protoc-gen-go-http`,
  `protoc-gen-go-middleware`, `protoc-gen-openapi`).

## Conventions

- Forge is pre-v1 and takes breaking changes deliberately. Do not add an alias
  or a shim to preserve an old name; change the callers.
- Documentation and code comments describe the current design only. Do not write
  migration narratives, "previously this was…", or changelog prose in them —
  that history belongs in Git commits.
- Design documents in `docs/design/` carry the rationale and the rejected
  alternatives. The guides in `docs/agent/` carry the rules and the examples.
  When they disagree, the code is right and both should be fixed.
