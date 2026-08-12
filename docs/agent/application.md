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
	httpSrv := forgehttp.NewServer(forgehttp.WithAddress(":8000"))
	grpcSrv := forgegrpc.NewServer(forgegrpc.WithAddress(":9000"))

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

`forge.New` generates a UUID service id unless `forge.WithID` sets one, and
defaults the stop signals to `SIGTERM`, `SIGQUIT`, `SIGINT` unless
`forge.WithSignal` overrides them. `forge.WithLogger(logger)` also installs the logger
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

1. build the registry instance from `WithEndpoint`, or from each server's
   `Endpointer` when no endpoint was configured,
2. `BeforeStart` hooks, in registration order,
3. every server's `Start`, concurrently,
4. `Registrar.Register`, bounded by `WithRegistrarTimeout` (default 10s),
5. `AfterStart` hooks,
6. wait for a signal or context cancellation,
7. `BeforeStop` hooks, then deregister, then every server's `Stop`, each
   bounded by `WithStopTimeout` (default 10s),
8. `AfterStop` hooks, bounded in total by `WithAfterStopTimeout` (default 10s).

A hook returning an error aborts startup and unwinds through the remaining
stop path. `Run` joins every error it collected rather than reporting only the
first.

```go
app := forge.New(
	forge.WithName("helloworld"),
	forge.WithServer(httpSrv),
	forge.WithRegistrar(reg),
	forge.WithRegistrarTimeout(5*time.Second),
	forge.WithStopTimeout(15*time.Second),
	forge.WithAfterStopTimeout(5*time.Second),
	forge.WithBeforeStart(func(ctx context.Context) error { return pool.Ping(ctx) }),
	forge.WithAfterStop(func(ctx context.Context) error { return pool.Close() }),
)
```

The three timeouts are independent: `WithStopTimeout` bounds each server's
shutdown, `WithAfterStopTimeout` bounds all `AfterStop` hooks together. A
non-positive `WithAfterStopTimeout` disables that deadline.

Inside a handler or hook, recover application identity from the context:

```go
if info, ok := forge.FromContext(ctx); ok {
	_ = info.Name()
	_ = info.Endpoints()
}
```

## Suites

A `Suite` bundles options that belong together — an integration and its
lifecycle hooks — so an application adopts them in one call:

```go
type tracingSuite struct{ provider trace.TracerProvider }

func (s tracingSuite) Options() []forge.Option {
	return []forge.Option{
		forge.WithAfterStop(func(ctx context.Context) error {
			return s.provider.(*sdktrace.TracerProvider).Shutdown(ctx)
		}),
	}
}

app := forge.New(
	forge.WithName("helloworld"),
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
c, err := config.New(ctx, config.WithSource(file.NewSource("./configs")))
if err != nil {
	return err
}
defer c.Close()

var bc conf.Bootstrap
if err := c.Scan(&bc); err != nil {
	return err
}

port, err := config.Get[int](c, "server.http.port")
```

`config.New` loads every source before returning: when it returns without
error the snapshot is complete and being kept current by one watch loop per
source, so there is no separate load step and no half-initialized Config. The
context bounds construction only; the watch loops run until `Close`.
`Value(key)` returns a typed accessor; `config.Get[T]` is the generic
shorthand. Watch a key for dynamic reconfiguration:

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
| `c := config.New(...)` then `c.Load()` | `config.New(ctx, ...)` returns `(*Config, error)` and loads on construction; there is no `Load` |
| Assuming `Healthz()` reports liveness | It reports readiness; liveness is the process |
| Calling `srv.Stop` directly for a drain | Implement/prefer `GracefulStopper`; the lifecycle uses it |
| Parsing `Transporter.Operation()` | Opaque — dispatch on `Kind()` |
| Expecting `WithStopTimeout` to bound `AfterStop` | Separate knob: `WithAfterStopTimeout` |
| Mutating a `Suite` after `WithSuite` | `Options` is read immediately |
