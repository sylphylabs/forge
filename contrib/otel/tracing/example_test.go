package tracing_test

// The examples in this file mirror the snippets in
// docs/agent/observability.md so that the guide cannot drift from the API
// without breaking the build. When one of these stops compiling, fix the
// guide together with the example.

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	otellog "github.com/sylphylabs/forge/contrib/otel/log"
	"github.com/sylphylabs/forge/contrib/otel/metrics"
	"github.com/sylphylabs/forge/contrib/otel/tracing"
	v1 "github.com/sylphylabs/forge/internal/testdata/helloworld"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/middleware"
	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
	forgehttp "github.com/sylphylabs/forge/transport/http"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/sylphylabs/forge"
)

// tracingSuite mirrors the Suite snippet in docs/agent/application.md: an
// integration bundles its lifecycle hooks so an application adopts them in
// one WithSuite call.
type tracingSuite struct{ provider trace.TracerProvider }

func (s tracingSuite) Options() []forge.Option {
	return []forge.Option{
		forge.WithAfterStop(func(ctx context.Context) error {
			return s.provider.(*sdktrace.TracerProvider).Shutdown(ctx)
		}),
	}
}

func Example_suite() {
	tp := sdktrace.NewTracerProvider()

	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithSuite(tracingSuite{provider: tp}),
	)

	_ = app
	fmt.Println("constructed")
	// Output: constructed
}

type greeter struct{}

func (greeter) SayHello(_ context.Context, req *v1.HelloRequest) (*v1.HelloReply, error) {
	return &v1.HelloReply{Message: "hello " + req.GetName()}, nil
}

// newServer mirrors "Wiring all three together": providers are constructed
// and shut down by the application; the three instrumentation points are
// independent of each other and attach differently — tracing is middleware,
// HTTP metrics is a filter, logging is an slog.Handler.
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
		forgehttp.WithAddress(":8000"),
		forgehttp.WithFilter(serverMetrics),
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

func Example_wiring() {
	srv, err := newServer(greeter{}, tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider())
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = srv
	fmt.Println("wired")
	// Output: wired
}

// Example_traceContext mirrors "Tracing": reading trace context anywhere
// downstream. With no active span the helpers return zero values.
func Example_traceContext() {
	ctx := context.Background()

	fmt.Println(tracing.TraceID(ctx) == "") // "" when there is no active span
	fmt.Println(tracing.SpanID(ctx) == "")
	fmt.Println(len(tracing.TraceAttrs(ctx))) // []slog.Attr{trace_id, span_id}
	// Output:
	// true
	// true
	// 2
}

// Example_clientMiddleware mirrors the guide's client-side attach points:
// tracing.Client goes through WithClientMiddleware on either transport.
func Example_clientMiddleware() {
	ctx := context.Background()
	endpoint := "http://127.0.0.1:8000"

	client, err := forgehttp.NewClient(ctx,
		forgehttp.WithTarget(endpoint),
		forgehttp.WithClientMiddleware(tracing.Client()),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	conn, err := forgegrpc.NewClient(ctx,
		forgegrpc.WithTarget("dns:///example.invalid:9000"),
		forgegrpc.WithClientMiddleware(tracing.Client()),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer conn.Close()

	fmt.Println("constructed")
	// Output: constructed
}

// reporterHub abstracts the vendor SDK hub from the guide's "Route B" error
// reporting middleware (sentry.CurrentHub().Clone() in the guide). The
// middleware shape below is the part Forge's API is responsible for; the
// vendor SDK calls are application wiring.
type reporterHub interface {
	SetTag(key, value string)
	RecoverWithContext(ctx context.Context, cause any)
}

// errorReporter mirrors the guide's sentryReporter: it tags trace_id so the
// issue joins its trace, and it re-panics so recovery middleware placed
// before it in the plan still maps the panic to a response.
func errorReporter(hub reporterHub) middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			hub.SetTag("trace_id", tracing.TraceID(ctx))
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

type nopHub struct{}

func (nopHub) SetTag(string, string)                   {}
func (nopHub) RecoverWithContext(context.Context, any) {}

func Example_errorReporter() {
	mw := errorReporter(nopHub{})
	handler := mw(func(_ context.Context, _ any) (any, error) { return "ok", nil })

	reply, err := handler(context.Background(), nil)
	fmt.Println(reply, err)
	// Output: ok <nil>
}
