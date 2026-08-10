# Observability

OpenTelemetry integration lives in the **separate module**
`github.com/sylphylabs/forge/contrib/otel`, so the root module carries no
OpenTelemetry SDK dependency. Add it explicitly:

```shell
go get github.com/sylphylabs/forge/contrib/otel
```

Four packages, each independent:

| Package | Gives you |
| --- | --- |
| `contrib/otel/tracing` | server/client tracing middleware, trace IDs on errors |
| `contrib/otel/metrics` | HTTP server/client duration metrics (semconv v1.41) |
| `contrib/otel/log` | `slog` handler bridging to the OTel logs SDK |
| `contrib/otel/message` | producer/consumer spans for `transport/message` |

**The SDK is yours.** Forge never creates, configures, or shuts down a
`TracerProvider`, `MeterProvider`, or `LoggerProvider`. Readers, exporters,
resources, views, bucket boundaries, cardinality limits, and exemplar filters
are all application configuration. Forge only consumes the providers you pass.

## Wiring all three together

Providers are constructed and shut down by the application; the three
instrumentation points are independent of each other.

```go
package main

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	otellog "github.com/sylphylabs/forge/contrib/otel/log"
	"github.com/sylphylabs/forge/contrib/otel/metrics"
	"github.com/sylphylabs/forge/contrib/otel/tracing"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/middleware"
	forgehttp "github.com/sylphylabs/forge/transport/http"

	v1 "example.com/api/helloworld/v1"
)

func newServer(
	greeter v1.GreeterHTTPServer,
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
) (*forgehttp.Server, error) {
	// Logs: bridge slog to the OTel logs SDK. The handler stamps the ambient
	// trace and span ID onto every record, so logs join traces automatically.
	logger := log.NewLogger(otellog.NewHandler("helloworld")).
		With(slog.String("service.name", "helloworld"))

	// Metrics: HTTP server duration is a net/http filter, not middleware.
	serverMetrics, err := metrics.NewHTTPServerFilter(meterProvider)
	if err != nil {
		return nil, err
	}

	srv := forgehttp.NewServer(
		forgehttp.Address(":8000"),
		forgehttp.Filter(serverMetrics),
	)

	// Tracing: middleware, attached through the generated plan.
	service, err := v1.WrapGreeterHTTPServer(greeter, v1.GreeterMiddleware{
		Unary: []middleware.UnaryMiddleware{
			tracing.Server(
				tracing.WithTracerProvider(tracerProvider),
				tracing.WithPropagator(propagation.TraceContext{}),
			),
		},
	})
	if err != nil {
		return nil, err
	}
	v1.RegisterGreeterHTTPServer(srv, service)

	_ = logger
	return srv, nil
}
```

Note the three attach points differ, and this is the thing to get right:

- **tracing** is `middleware.UnaryMiddleware` → goes in the generated plan
  (see [middleware.md](middleware.md)),
- **HTTP metrics** is a `forgehttp.FilterFunc` → goes in `forgehttp.Filter(...)`,
- **logging** is an `slog.Handler` → goes in `log.NewLogger(...)`.

## Tracing

```go
tracing.Server(opts ...Option) middleware.UnaryMiddleware
tracing.Client(opts ...Option) middleware.UnaryMiddleware
```

Options: `WithTracerProvider`, `WithPropagator`, `WithTracerName`. With no
provider it uses the global one set by `otel.SetTracerProvider`.

`tracing.Server` also **stamps the ambient trace ID onto every outgoing error**.
This is what makes a failure diagnosable across a process boundary, since the
cause chain deliberately does not cross one (see [errors.md](errors.md)). An
error that already carries a trace keeps it, and an error received from another
service is never re-stamped — the value closest to the failure is the precise
one.

Reading trace context anywhere downstream:

```go
tracing.TraceID(ctx)    // "" when there is no active span
tracing.SpanID(ctx)
tracing.TraceAttrs(ctx) // []slog.Attr{trace_id, span_id}
```

Client-side, attach `tracing.Client(...)` through
`forgehttp.WithMiddleware(...)` or `forgegrpc.WithMiddleware(...)`.

## HTTP metrics

The package emits exactly two instruments —
`http.server.request.duration` and `http.client.request.duration` — under
semantic conventions schema v1.41. The provider is required.

```go
serverMetrics, err := metrics.NewHTTPServerFilter(provider)
if err != nil {
	return err
}
srv := forgehttp.NewServer(forgehttp.Filter(serverMetrics))

clientMetrics, err := metrics.NewHTTPClientWrapper(provider)
if err != nil {
	return err
}
client, err := forgehttp.NewClient(ctx,
	forgehttp.WithEndpoint(endpoint),
	forgehttp.WithRoundTripperWrapper(clientMetrics),
)
```

Both constructors return an error — do not discard it. Unrecognized HTTP methods
collapse to `_OTHER` to bound cardinality; extend the set with
`metrics.WithHTTPServerKnownMethods(...)` or
`metrics.WithHTTPClientKnownMethods(...)`.

See [docs/design/otel-metrics.md](../design/otel-metrics.md) for the lifecycle,
status, and cardinality contract.

## gRPC metrics

Forge does **not** implement gRPC metrics. Use grpc-go's official A66 stats
integration and pass it through Forge's native option hooks. Use an explicit
metric set — not nil, not the defaults:

```go
import (
	grpcstats "google.golang.org/grpc/stats"
	grpcotel "google.golang.org/grpc/stats/opentelemetry"

	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
)

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

srv := forgegrpc.NewServer(forgegrpc.Options(grpcotel.ServerOption(otelOptions)))
conn, err := forgegrpc.NewClient(ctx, forgegrpc.WithOptions(grpcotel.DialOption(otelOptions)))
```

## Logs

`otellog.NewHandler` returns an `slog.Handler` that writes through the OTel logs
SDK and attaches trace correlation:

```go
logger := log.NewLogger(otellog.NewHandler("helloworld"))
```

Options: `WithLoggerProvider`, `WithVersion`, `WithSchemaURL`, `WithSource`.
Compose with the core log builder for fixed attributes and redaction:

```go
logger := log.NewLogger(
	otellog.NewHandler("helloworld"),
	log.WithFilter(log.FilterKey("password")),
).With(slog.String("service.name", "helloworld"))
```

Pass the result to `forge.Logger(logger)` to make it the application default.

## Error reporting

Forge ships **no error-tracking module**. Reporting a panic or an error to an
issue tracker is application wiring, not framework surface — the same rule that
makes providers yours. Two routes exist, and the choice between them is not
cosmetic.

### Route A — OTLP, no vendor SDK

Sentry accepts OTLP for **traces and logs** (not metrics), so `contrib/otel`
alone can target it. Point the exporter at the ingest endpoint; credentials come
from Project Settings → Client Keys (DSN) → OpenTelemetry (OTLP):

```shell
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=https://o{ORG_ID}.ingest.us.sentry.io/api/{PROJECT_ID}/integration/otlp/v1/traces
```

**This route does not give you issues.** OTLP ingestion drops span events, and
`span.RecordError(err)` in Go *is* a span event — so recorded errors never become
grouped, deduplicated issues. You get traces. OpenTelemetry is moving exceptions
from span events to the Logs API, but no language SDK ships a stable log-based
equivalent yet.

### Route B — vendor SDK middleware

For an issue inbox, report through the vendor SDK. The middleware is short
enough to own:

```go
import (
	"context"

	"github.com/getsentry/sentry-go"

	"github.com/sylphylabs/forge/contrib/otel/tracing"
	"github.com/sylphylabs/forge/middleware"
)

func sentryReporter() middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			hub := sentry.CurrentHub().Clone()
			hub.Scope().SetTag("trace_id", tracing.TraceID(ctx))
			defer func() {
				if cause := recover(); cause != nil {
					hub.RecoverWithContext(ctx, cause)
					panic(cause)
				}
			}()
			return handler(ctx, req)
		}
	}
}
```

Two things this snippet is doing deliberately:

- **It re-panics.** Place it *after* `recovery.Recovery()` in the plan (see
  [middleware.md](middleware.md)) so the panic is observed here and still mapped
  to a response there. Swallowing it would strip the client's error.
- **It tags `trace_id`.** Because Route A drops span events, this tag is the only
  thing joining the issue to its trace. It is required, not decorative.

Own the scope fields — tenant, user, business operation — rather than reporting
whatever the transport happens to expose; the useful attributes are
application-specific.

The application owns `sentry.Init` and `defer sentry.Flush(...)`. Read the DSN
from configuration and leave it **empty outside production**: an empty DSN makes
every SDK call a no-op, so panics surface in the terminal during development. Set
`Environment` and `Release`, or staging noise and production incidents group
together and regressions cannot be attributed to a deploy.

Because this route speaks the Sentry SDK protocol, the same code targets
self-hosted [GlitchTip](https://glitchtip.com/) or
[Bugsink](https://www.bugsink.com/) by changing the DSN alone.

## Asynchronous messages

Instrumentation for `transport/message` is an explicit decorator — it never
infers the broker from the wrapped implementation, so `WithSystem` is yours to
set:

```go
publisher := messageotel.NewPublisher(next,
	messageotel.WithTracerProvider(provider),
	messageotel.WithPropagator(propagation.TraceContext{}),
	messageotel.WithSystem("nats"),
)

server := message.NewServer(subscriber,
	message.WithMiddleware(messageotel.Consumer(
		messageotel.WithTracerProvider(provider),
		messageotel.WithPropagator(propagation.TraceContext{}),
		messageotel.WithSystem("nats"),
	)),
)
```

The publisher creates a producer span and injects trace context into a **clone**
of the message, preserving the caller's body and headers. The consumer extracts
it and creates a child span. Only low-cardinality messaging attributes and an
optional message ID are recorded — payloads and arbitrary headers are never
copied into spans. Errors are recorded on the span and returned unchanged.

## Never write these

| Wrong | Right |
| --- | --- |
| `grpc.NewServer(grpc.Middleware(tracing.Server()))` | Put `tracing.Server()` in the generated middleware plan |
| `forgehttp.NewServer(forgehttp.Middleware(...))` | No such option; tracing goes in the plan, metrics in `Filter` |
| Importing `contrib/otel/...` and expecting the root `go.mod` to resolve it | It is a separate module; `go get` it |
| `metrics.NewHTTPServerFilter(nil)` | The provider is required |
| Discarding the `error` from the metrics constructors | Both return `(_, error)` |
| Building a `MeterProvider` inside Forge | Providers are application-owned, including shutdown |
| Expecting Forge to emit gRPC metrics | Use grpc-go A66 with an explicit metric set |
| `go get github.com/sylphylabs/forge/contrib/errortracker/sentry` | No such module; wire the error-tracking SDK in the application |
| Expecting `span.RecordError` to become an issue over OTLP | OTLP ingestion drops span events; use the vendor SDK for issues |
| Reporting a panic without re-panicking | `recovery.Recovery()` must still map it to a response |
