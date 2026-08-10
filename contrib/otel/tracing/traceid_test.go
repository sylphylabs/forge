package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/trace"

	"github.com/sylphylabs/forge/errors"
)

func tracedCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	tp := trace.NewTracerProvider(trace.WithSampler(trace.AlwaysSample()))
	ctx, span := tp.Tracer("t").Start(t.Context(), "op")
	t.Cleanup(func() { span.End() })
	return ctx, span.SpanContext().TraceID().String()
}

func TestWithTraceIDStamps(t *testing.T) {
	ctx, want := tracedCtx(t)
	err := withTraceID(ctx, errors.New(errors.KindInternal).WithReason("BOOM"))
	if got := errors.FromError(err).TraceID(); got != want {
		t.Errorf("trace ID = %q, want %q", got, want)
	}
}

func TestWithTraceIDPreservesExisting(t *testing.T) {
	ctx, _ := tracedCtx(t)
	err := withTraceID(ctx, errors.New(errors.KindInternal).WithTraceID("closer-to-failure"))
	if got := errors.FromError(err).TraceID(); got != "closer-to-failure" {
		t.Errorf("trace ID = %q, want the existing one kept", got)
	}
}

// A remote error names the callee's trace; re-stamping would point an operator
// at the wrong service.
func TestWithTraceIDLeavesRemoteAlone(t *testing.T) {
	ctx, ambient := tracedCtx(t)
	remote := errors.FromPublic(errors.Public{Kind: errors.KindInternal, Domain: "test.v1", Reason: "R"})
	err := withTraceID(ctx, remote)
	if got := errors.FromError(err).TraceID(); got == ambient {
		t.Error("re-stamped a remote error with the local trace")
	}
}

func TestWithTraceIDNilAndUntraced(t *testing.T) {
	ctx, _ := tracedCtx(t)
	if withTraceID(ctx, nil) != nil {
		t.Error("nil error must stay nil")
	}
	plain := errors.New(errors.KindInternal)
	if got := withTraceID(t.Context(), plain); errors.FromError(got).TraceID() != "" {
		t.Error("no ambient trace must leave the error unchanged")
	}
}
