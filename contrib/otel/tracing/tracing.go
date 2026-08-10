package tracing

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// Option is tracing option.
type Option func(*options)

type options struct {
	tracerName     string
	tracerProvider trace.TracerProvider
	propagator     propagation.TextMapPropagator
}

// WithPropagator with tracer propagator.
func WithPropagator(propagator propagation.TextMapPropagator) Option {
	return func(opts *options) {
		opts.propagator = propagator
	}
}

// WithTracerProvider with tracer provider.
// By default, it uses the global provider that is set by otel.SetTracerProvider(provider).
func WithTracerProvider(provider trace.TracerProvider) Option {
	return func(opts *options) {
		opts.tracerProvider = provider
	}
}

// WithTracerName with tracer name
func WithTracerName(tracerName string) Option {
	return func(opts *options) {
		opts.tracerName = tracerName
	}
}

// Server returns a new server middleware for OpenTelemetry.
//
// A failing call leaves with its trace ID attached, so a caller that reports
// one lets an operator find the unredacted error in this service's logs. That
// correlation is the supported way to follow a failure across a process
// boundary, because the cause chain deliberately does not cross one.
func Server(opts ...Option) middleware.UnaryMiddleware {
	tracer := NewTracer(trace.SpanKindServer, opts...)
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				var span trace.Span
				ctx, span = tracer.Start(ctx, tr.Operation(), tr.RequestHeader())
				setServerSpan(ctx, span, req)
				defer func() { tracer.End(ctx, span, reply, err) }()
			}
			reply, err = handler(ctx, req)
			return reply, withTraceID(ctx, err)
		}
	}
}

// withTraceID stamps the ambient trace onto an outgoing error.
//
// An error that already names a trace keeps it: the value closest to the
// failure is the more precise one. An error from another service is left alone
// for the same reason — re-stamping it would replace the callee's trace with
// this one and point an operator at the wrong service.
func withTraceID(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	id := TraceID(ctx)
	if id == "" {
		return err
	}
	var e *errors.Error
	if !errors.As(err, &e) || e == nil || e.IsRemote() || e.TraceID() != "" {
		return err
	}
	return e.WithTraceID(id)
}

// Client returns a new client middleware for OpenTelemetry.
func Client(opts ...Option) middleware.UnaryMiddleware {
	tracer := NewTracer(trace.SpanKindClient, opts...)
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if tr, ok := transport.FromClientContext(ctx); ok {
				var span trace.Span
				ctx, span = tracer.Start(ctx, tr.Operation(), tr.RequestHeader())
				setClientSpan(ctx, span, req)
				defer func() { tracer.End(ctx, span, reply, err) }()
			}
			return handler(ctx, req)
		}
	}
}

// TraceID returns the trace ID from ctx.
func TraceID(ctx context.Context) string {
	if span := trace.SpanContextFromContext(ctx); span.HasTraceID() {
		return span.TraceID().String()
	}
	return ""
}

// SpanID returns the span ID from ctx.
func SpanID(ctx context.Context) string {
	if span := trace.SpanContextFromContext(ctx); span.HasSpanID() {
		return span.SpanID().String()
	}
	return ""
}

// TraceAttrs returns slog attributes for the trace and span IDs in ctx.
func TraceAttrs(ctx context.Context) []slog.Attr {
	return []slog.Attr{
		slog.String("trace_id", TraceID(ctx)),
		slog.String("span_id", SpanID(ctx)),
	}
}
