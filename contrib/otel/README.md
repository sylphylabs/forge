# OpenTelemetry contrib

This module keeps OpenTelemetry integrations out of the core Kratos module.

## Packages

- `github.com/openkratos/kratos/contrib/otel/log`: slog handler bridge, usually imported as `otel` for `otel.NewHandler`.
- `github.com/openkratos/kratos/contrib/otel/tracing`: tracing middleware and trace slog attributes.
- `github.com/openkratos/kratos/contrib/otel/metrics`: stable HTTP semantic-convention metrics; gRPC uses grpc-go's A66 integration directly.
- `github.com/openkratos/kratos/contrib/otel/message`: producer/consumer spans and
  trace-context propagation for the protocol-neutral `transport/message` API.

## Metrics

HTTP metrics instrument the native server and client lifecycles. The provider is
required and remains owned by the application:

```go
serverMetrics, err := metrics.NewHTTPServerFilter(provider)
if err != nil {
	return err
}
server := kratoshttp.NewServer(kratoshttp.Filter(serverMetrics))

clientMetrics, err := metrics.NewHTTPClientWrapper(provider)
if err != nil {
	return err
}
client, err := kratoshttp.NewClient(
	ctx,
	kratoshttp.WithEndpoint(endpoint),
	kratoshttp.WithRoundTripperWrapper(clientMetrics),
)
```

The package emits only `http.server.request.duration` and
`http.client.request.duration`, using semantic conventions schema v1.41. SDK
readers, exporters, resources, Views, bucket overrides, cardinality limits, and
exemplar filtering are application configuration.

For gRPC, pass grpc-go's official A66 stats options through OpenKratos native
option hooks. Use an explicit metric set containing only
`grpc.client.call.duration`, `grpc.client.attempt.duration`, and
`grpc.server.call.duration`; do not use nil or default metrics:

```go
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

server := kratosgrpc.NewServer(
	kratosgrpc.Options(grpcotel.ServerOption(otelOptions)),
)
conn, err := kratosgrpc.NewClient(
	ctx,
	kratosgrpc.WithOptions(grpcotel.DialOption(otelOptions)),
)
```

See the [metrics contract](../../docs/design/otel-metrics.md) for lifecycle,
status, cardinality, and migration requirements.

## Asynchronous Messages

Message instrumentation is an optional decorator. It keeps broker SDKs and
OpenTelemetry out of the root module, and it does not infer a broker from the
wrapped implementation:

```go
import (
	messageotel "github.com/openkratos/kratos/contrib/otel/message"
	"github.com/openkratos/kratos/transport/message"
)

publisher := messageotel.NewPublisher(
	nextPublisher,
	messageotel.WithTracerProvider(provider),
	messageotel.WithPropagator(propagation.TraceContext{}),
	messageotel.WithSystem("nats"),
)

server := message.NewServer(
	subscriber,
	message.WithMiddleware(messageotel.Consumer(
		messageotel.WithTracerProvider(provider),
		messageotel.WithPropagator(propagation.TraceContext{}),
		messageotel.WithSystem("nats"),
	)),
)
```

The publisher creates a producer span, injects its context into a cloned
message, and preserves the caller's body and headers. Consumer middleware
extracts that context and creates a child process span. Errors are recorded and
mark the span as failed; the original handler or publisher error is returned
unchanged. The default provider and propagator are local no-op/TraceContext
instances, so applications that need export or baggage must pass them
explicitly. Only low-cardinality messaging attributes and an optional message
ID are recorded; payloads and arbitrary headers are never copied into spans.

## Logger

```go
import (
	otel "github.com/openkratos/kratos/contrib/otel/log"
	"github.com/openkratos/kratos/log"
)

logger := log.NewLogger(otel.NewHandler("helloworld"))
```

Use the core Kratos log builder when the logger also needs fixed attrs or
filtering:

```go
import (
	"log/slog"

	otel "github.com/openkratos/kratos/contrib/otel/log"
	"github.com/openkratos/kratos/log"
)

logger := log.NewLogger(
	otel.NewHandler("helloworld"),
	log.WithFilter(log.FilterKey("password")),
).With(slog.String("service.name", "helloworld"))
```

Log, tracing, metrics, and message instrumentation stay in shallow optional
subpackages so the root module does not depend on OpenTelemetry SDK packages.
