# OpenTelemetry Metrics Contract

Status: accepted

Last reviewed: July 25, 2026

## Decision

OpenKratos exposes transport-native, duration-only OpenTelemetry metrics. HTTP
instrumentation follows the stable OpenTelemetry HTTP semantic conventions
v1.41. gRPC instrumentation is delegated to grpc-go's gRFC A66 implementation.
The two protocols do not share names or attributes, and OpenKratos does not
emit a second compatibility metric stream.

This document is normative for the public API, instrument identity, request
lifecycle, attribute set, and dependency ownership. Applications remain
responsible for SDK policy such as readers, exporters, resources, views,
cardinality limits, aggregation overrides, and exemplar filtering.

## Design Rationale

The first version deliberately exposes only duration histograms. A histogram
already provides request count, outcome dimensions, and a latency distribution,
which is sufficient to derive the rate, errors, and duration parts of RED.
Adding a request counter with the same attributes would duplicate the
histogram's count without adding information.

Latency distributions are retained because averages hide tail behavior. This
is particularly important in fan-out systems, where a small slow fraction can
dominate end-to-end latency. The default boundaries cover short in-process and
network calls through long request deadlines without synthesizing samples or
attempting coordinated-omission correction in production instrumentation.

Active requests are concurrency, not resource saturation. Saturation requires
a defined capacity such as a worker pool, queue, connection pool, CPU, or disk.
Consequently, active-request and body-size instruments require a separate RFC
with an explicit operational use case. USE remains applicable to those
resources, not to a generic HTTP request count.

## Ownership Boundary

The instrumentation library MUST:

- require an explicit `metric.MeterProvider`;
- create only the instruments defined in this document;
- use the request context when recording, so an SDK can attach exemplars from a
  sampled span;
- bound every emitted attribute according to this document; and
- return construction errors before a server or client begins handling work.

The instrumentation library MUST NOT read the global MeterProvider, silently
substitute a no-op provider, create an SDK reader or exporter, mutate process
environment variables, or install an SDK View. Tests and applications that
need disabled metrics pass `metric/noop.NewMeterProvider()` explicitly.

The application owns:

- the SDK `MeterProvider` and its shutdown;
- resources and service identity;
- readers and exporters;
- Views, aggregation and bucket overrides;
- cardinality limits and memory policy; and
- exemplar filtering, including `OTEL_METRICS_EXEMPLAR_FILTER` when environment
  configuration is used.

This separation permits two applications in one process to use independent
providers without observing or resetting shared telemetry state.

## HTTP API

The HTTP package is
`github.com/openkratos/kratos/contrib/otel/metrics`:

```go
func NewHTTPServerFilter(
	provider metric.MeterProvider,
	opts ...HTTPServerOption,
) (http.FilterFunc, error)

func NewHTTPClientWrapper(
	provider metric.MeterProvider,
	opts ...HTTPClientOption,
) (http.RoundTripperWrapper, error)

func WithHTTPServerKnownMethods(methods ...string) HTTPServerOption
func WithHTTPClientKnownMethods(methods ...string) HTTPClientOption
```

The core HTTP transport provides a general construction-time decorator:

```go
type RoundTripperWrapper func(nethttp.RoundTripper) (nethttp.RoundTripper, error)

func WithRoundTripperWrapper(wrappers ...RoundTripperWrapper) ClientOption
```

Repeated `WithRoundTripperWrapper` options append. Wrappers are applied after
the base transport is selected and any TLS transport clone is complete. The
first wrapper is outermost. A nil wrapper, nil base transport, nil wrapped
transport, or instrument creation failure MUST cause construction to return a
descriptive error. There is no `Must` constructor.

Example:

```go
serverFilter, err := metrics.NewHTTPServerFilter(provider)
if err != nil {
	return err
}
server := kratoshttp.NewServer(kratoshttp.Filter(serverFilter))

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

The metrics server filter belongs in the global server filter chain so it
covers routing, transport filters, binding, service execution, encoding, and
error encoding. Per-route installation does not implement this contract.

## HTTP Instruments

Both instruments are `Float64Histogram` values with unit `s`:

| Instrument | Required attributes | Conditional attributes |
| --- | --- | --- |
| `http.server.request.duration` | `http.request.method`, `url.scheme` | `http.response.status_code`, `http.route`, `error.type`, `network.protocol.version` |
| `http.client.request.duration` | `http.request.method`, `server.address`, `server.port` | `http.response.status_code`, `error.type`, `network.protocol.version` |

The Meter instrumentation scope MUST be the package import path. It MUST use
schema URL `https://opentelemetry.io/schemas/1.41.0`.

The instruments use the OpenTelemetry-recommended advisory boundaries:

```text
[0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25,
 0.5, 0.75, 1, 2.5, 5, 7.5, 10]
```

An application may replace these through an SDK View. OpenKratos does not
expose a View helper because Views are an SDK deployment decision, not an
instrumentation-library API.

### Method Policy

The default known-method set is `CONNECT`, `DELETE`, `GET`, `HEAD`, `OPTIONS`,
`PATCH`, `POST`, `PUT`, `QUERY`, and `TRACE`, covering the methods recognized by
the semantic conventions from RFC 9110, RFC 5789, and RFC 10008.

Known-method options are case-sensitive complete replacements. They do not
append to the default. An empty replacement maps every request method to
`_OTHER`. Every supplied value MUST be a non-empty valid HTTP token; invalid
configuration is rejected by the constructor. A method outside the configured
set is emitted as `_OTHER`; its raw value is never emitted as another
attribute.

### Server Lifecycle

`http.server.request.duration` begins when the global filter receives the
request and ends when the complete `ServeHTTP` call returns or panics. It
therefore includes routing, request binding, handler work, response encoding,
and writes performed before `ServeHTTP` returns. It does not measure how long a
kernel or downstream client takes to consume buffered bytes.

The response writer is observed with `github.com/felixge/httpsnoop` v1.1.0 or
newer. The wrapper MUST preserve `Flusher`, `Hijacker`, `Pusher`, `ReaderFrom`,
`StringWriter`, `ResponseController` behavior, and `Unwrap`.

Status capture follows `net/http` semantics with these additional rules:

- temporary 1xx responses do not become the final status;
- an explicit `101 Switching Protocols` is final;
- `Write`, `ReadFrom`, and `Flush` implicitly commit 200;
- a normal return without a write records 200;
- a panic before commitment does not invent a 200, records `error.type` as
  `_OTHER`, and is re-panicked unchanged; and
- a successful hijack infers 101 only for a syntactically valid HTTP Upgrade
  request; ordinary CONNECT and unknown hijacks do not guess a status.

The server emits `url.scheme` from the actual connection (`https` when TLS is
present, otherwise `http`) and does not trust forwarded headers by default.

### Canonical Routes

`http.route` is emitted only after the final registered route candidate matches.
It is the canonical registered Google path template, for example
`/v1/{name=projects/*/locations/*}`, and never the request path. The router
clears the standard `http.ServeMux` scratch pattern on entry and sets the
canonical template on the same request only after a successful final match.

404, 405, route errors, and candidates rejected by path constraints have no
`http.route`. Internal `ServeMux` templates such as `{__openkratos0}`, raw URL
paths, and query strings MUST NOT be used as fallbacks.

### Client Lifecycle

`http.client.request.duration` starts immediately before calling the selected
base `RoundTripper.RoundTrip`. It ends when response headers are returned or a
transport error is returned. It includes request upload time but excludes
response-body reads, OpenKratos response/error decoding, and redirect work
performed by other `RoundTrip` calls. The request context, method, address, and
port attributes are snapshotted before invoking the base transport, so an inner
wrapper cannot mutate the recorded identity of the attempt.

For direct requests and redirects, the client derives `server.address` and
`server.port` from that `RoundTrip` request's URL. For the initial request to a
discovery endpoint, it uses the configured logical service authority and the
selected HTTP or HTTPS scheme; it never exposes the selected physical node.
Address parsing handles DNS names and bracketed IPv6 addresses and materializes
the scheme's default port when the authority omits it. A user-controlled Host
header or URL path is never used as the server address.

### Status and Error Classification

The server records 4xx as status only. Server 5xx records both
`http.response.status_code` and `error.type` equal to the decimal status code.
The client records `error.type` as the decimal status code for both 4xx and 5xx.

Transport errors use the bounded value returned by `semconv.ErrorType(err)`.
The error message, wrapped error string, and Kratos `reason` are never metric
attributes. A panic uses `_OTHER`. Successful requests have no `error.type`.

## gRPC A66 Integration

OpenKratos does not add a gRPC metrics wrapper. Applications pass grpc-go's
official OpenTelemetry stats options through the existing native option hooks:

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

The metric set MUST be explicit and contain exactly:

- `grpc.client.call.duration`;
- `grpc.client.attempt.duration`; and
- `grpc.server.call.duration`.

Leaving `Metrics` nil or using `DefaultMetrics()` is non-conforming because it
enables started and message-size instruments and can enable new defaults after
a grpc-go upgrade. `TraceOptions` remains its zero value. Existing OpenKratos
tracing may be installed independently and MUST NOT be duplicated through the
stats option.

A client logical call has one call-duration point and one attempt-duration
point for each retry or hedging attempt. Server calls have one duration point.
grpc-go owns unary and all four streaming lifecycles; client stream completion
is reported through its official `grpc.OnFinish` integration.

Only grpc-go's A66 attributes `grpc.method`, `grpc.target`, and `grpc.status`
are emitted. Unregistered or dynamic methods are normalized to `other` by the
official implementation. OpenKratos does not emit RC `rpc.*` metrics or a
second set of gRPC instruments.

## Cardinality and Data Policy

The default instrument set MUST NOT emit:

- original URL, path, query, or `url.template`;
- server-side address or port;
- body or message sizes;
- active requests or started-call counters;
- `reason`, error messages, stack traces, user IDs, or tenant IDs;
- baggage or arbitrary user-supplied attributes; or
- unknown or dynamic method names.

Routes are bounded by the registered route set, known methods by explicit
configuration, status values by protocol registries, and error types by the
semantic-convention classifier. Deployments still SHOULD configure SDK
cardinality limits appropriate to their aggregation and exporter topology.

## Validation Contract

Tests use an independent `ManualReader` and `MeterProvider` per case and assert
structured metric data, attributes, values, scope, and schema. They do not
install global providers, use periodic readers, rely on random sleeps, or
search serialized output.

HTTP conformance covers status commitment, interim responses, panic, write
errors, upgrades, CONNECT, SSE, routing failures, complex templates, response
writer capabilities, TLS wrapping order, address parsing, cancellation,
transport errors, and response-body exclusion. gRPC conformance covers unary,
all streaming shapes, canonical status, unknown methods, and retries.

Negative assertions verify that legacy names, `reason`, `rpc.*`, started and
size instruments, unrequested opt-in attributes, and duplicate spans are
absent. Provider-isolation and sampled-context tests verify instance ownership
and exemplars. Benchmarks cover explicit no-op and recording providers; the
no-op path must add no request-level allocation.

## References

Normative:

- [OpenTelemetry HTTP Metrics semantic conventions v1.41](https://github.com/open-telemetry/semantic-conventions/blob/v1.41.0/docs/http/http-metrics.md)
- [OpenTelemetry Library Guidelines v1.56](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.56.0/specification/library-guidelines.md)
- [OpenTelemetry Metrics API](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.56.0/specification/metrics/api.md)
- [OpenTelemetry Metrics SDK](https://github.com/open-telemetry/opentelemetry-specification/blob/v1.56.0/specification/metrics/sdk.md)
- [gRFC A66: OpenTelemetry Metrics](https://github.com/grpc/proposal/blob/master/A66-otel-stats.md)
- [RFC 9110: HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html)
- [RFC 5789: PATCH Method for HTTP](https://www.rfc-editor.org/rfc/rfc5789.html)
- [RFC 10008: The HTTP QUERY Method](https://datatracker.ietf.org/doc/html/rfc10008)

Informative:

- [Google SRE: Monitoring Distributed Systems](https://sre.google/sre-book/monitoring-distributed-systems/)
- [The Tail at Scale](https://research.google/pubs/the-tail-at-scale/)
- [Prometheus Histograms and Summaries](https://prometheus.io/docs/practices/histograms/)
