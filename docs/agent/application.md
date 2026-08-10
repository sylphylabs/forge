# Application, transports, and config

Package `github.com/sylphylabs/forge` manages the lifecycle of a set of
transport servers. It does not dictate how you structure your project — there is
no scaffolding CLI and no required directory layout.

## Minimal service

```go
package main

import (
	"github.com/sylphylabs/forge"
	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

func main() {
	httpSrv := forgehttp.NewServer(forgehttp.Address(":8000"))
	grpcSrv := forgegrpc.NewServer(forgegrpc.Address(":9000"))

	app := forge.New(
		forge.Name("helloworld"),
		forge.Version("v1.0.0"),
		forge.Server(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
```

`forge.New` generates a UUID `ID` unless you set one, and defaults `Signal` to
`SIGTERM`, `SIGQUIT`, `SIGINT`. `forge.Logger(logger)` also installs the logger
as the `log` package default.

Register generated services on the transport servers before `Run` — wrapping
them with middleware first, per [middleware.md](middleware.md):

```go
service, err := v1.WrapGreeterHTTPServer(greeter, plan)
if err != nil {
	return err
}
v1.RegisterGreeterHTTPServer(httpSrv, service)
```

## Lifecycle

`Run` executes in this order, and returns only after everything below settles:

1. build the registry instance from `Endpoint`, or from each server's
   `Endpointer` when no endpoint was configured,
2. `BeforeStart` hooks, in registration order,
3. every server's `Start`, concurrently,
4. `Registrar.Register`, bounded by `RegistrarTimeout` (default 10s),
5. `AfterStart` hooks,
6. wait for a signal or context cancellation,
7. deregister, then every server's `Stop`, each bounded by `StopTimeout`
   (default 10s),
8. `AfterStop` hooks, bounded in total by `AfterStopTimeout` (default 10s).

A hook returning an error aborts startup and unwinds through the remaining
stop path. `Run` joins every error it collected rather than reporting only the
first.

```go
app := forge.New(
	forge.Name("helloworld"),
	forge.Server(httpSrv),
	forge.Registrar(reg),
	forge.RegistrarTimeout(5*time.Second),
	forge.StopTimeout(15*time.Second),
	forge.AfterStopTimeout(5*time.Second),
	forge.BeforeStart(func(ctx context.Context) error { return pool.Ping(ctx) }),
	forge.AfterStop(func(ctx context.Context) error { return pool.Close() }),
)
```

The three timeouts are independent: `StopTimeout` bounds each server's shutdown,
`AfterStopTimeout` bounds all `AfterStop` hooks together. A non-positive
`AfterStopTimeout` disables that deadline.

Inside a handler or hook, recover application identity from the context:

```go
if info, ok := forge.FromContext(ctx); ok {
	_ = info.Name()
	_ = info.Endpoint()
}
```

## Suites

A `Suite` bundles options that belong together — an integration and its
lifecycle hooks — so an application adopts them in one call:

```go
type tracingSuite struct{ provider trace.TracerProvider }

func (s tracingSuite) Options() []forge.Option {
	return []forge.Option{
		forge.AfterStop(func(ctx context.Context) error {
			return s.provider.(*sdktrace.TracerProvider).Shutdown(ctx)
		}),
	}
}

app := forge.New(
	forge.Name("helloworld"),
	forge.WithSuite(tracingSuite{provider: tp}),
)
```

`WithSuite` calls `Options` immediately and applies them **in place**, where the
returned option appears in the list — so ordinary last-wins semantics hold, and
you can override a suite by placing an option after it. It panics right away on
a nil suite or a nil option, so broken wiring fails at that line rather than
later. Suites compose: a suite's options may themselves include `WithSuite`.

## Transport server contract

```go
type Server interface {
	Start(context.Context) error
	Stop(context.Context) error
}
```

Everything else is an optional capability that consumers type-assert for. A
server that does not implement one makes no claim.

| Interface | Method | Meaning |
| --- | --- | --- |
| `Endpointer` | `Endpoint() (*url.URL, error)` | address to publish to the registry |
| `Healthzer` | `Healthz() bool` | **readiness**, not liveness: false before accepting traffic, false as soon as draining begins. Must not block |
| `GracefulStopper` | `GracefulStop(context.Context) error` | drain in-flight work; the lifecycle prefers it and falls back to `Stop` |
| `ReplyHeaderer` | `ReplyHeader() Header` | only for transports with a mutable reply header |

`Transporter` is what middleware sees. `Operation()` is **opaque** — use it for
labelling and keying, never parse it; dispatch on `Kind()` and read the concrete
transport type when you need structure.

`transport.KindHTTP` and `transport.KindGRPC` are provided, but `Kind` is an
open type: a transport outside this module may declare its own.

## Config

```go
c := config.New(config.WithSource(file.NewSource("./configs")))
defer c.Close()

if err := c.Load(); err != nil {
	return err
}

var bc conf.Bootstrap
if err := c.Scan(&bc); err != nil {
	return err
}

port, err := config.Get[int](c, "server.http.port")
```

`Load` must be called before reading. `Value(key)` returns a typed accessor;
`config.Get[T]` is the generic shorthand. Watch a key for dynamic reconfiguration:

```go
err := c.Watch("server.http.timeout", func(key string, value config.Value) {
	// applied on every source change
})
```

Sources are pluggable — `config/file` and `config/env` are built in, with
Apollo, Consul, etcd, Kubernetes, Nacos, and Polaris under `contrib/config/`.
Each contrib source is a separate module.

## Never write these

| Wrong | Right |
| --- | --- |
| `forge new myproject` / a scaffolding CLI | Forge ships none; use the Go toolchain and `buf generate` |
| Reading config before `Load()` | `Load()` first, then `Scan` / `Value` / `Get` |
| Assuming `Healthz()` reports liveness | It reports readiness; liveness is the process |
| Calling `srv.Stop` directly for a drain | Implement/prefer `GracefulStopper`; the lifecycle uses it |
| Parsing `Transporter.Operation()` | Opaque — dispatch on `Kind()` |
| Expecting `StopTimeout` to bound `AfterStop` | Separate knob: `AfterStopTimeout` |
| Mutating a `Suite` after `WithSuite` | `Options` is read immediately |
