package logging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

const (
	// fixedAttrs is the number of attributes every request record carries:
	// kind, component, operation, args, and latency.
	fixedAttrs = 5
	// maxErrorAttrs is the most attributes errorAttrs can return: error_kind,
	// reason, domain, trace_id, error, and stack.
	maxErrorAttrs = 6
)

// errorAttrs describes err for a log record.
//
// A failure is logged by kind and reason rather than by a transport status
// code: the kind is what the service decided, while a status code is a
// projection of it that differs per transport. The trace ID is included so that
// an operator handed one by a caller can find this record, which is what makes
// a redacted response diagnosable.
func errorAttrs(err error) []slog.Attr {
	if err == nil {
		return []slog.Attr{slog.String("error_kind", errors.KindUnknown.String())}
	}
	e := errors.FromError(err)
	attrs := []slog.Attr{
		slog.String("error_kind", e.Kind().String()),
		slog.String("reason", e.Reason()),
	}
	if domain := e.Domain(); domain != "" {
		attrs = append(attrs, slog.String("domain", domain))
	}
	if trace := e.TraceID(); trace != "" {
		attrs = append(attrs, slog.String("trace_id", trace))
	}
	// The full error, including the cause chain that never crosses the wire.
	attrs = append(attrs, slog.Any("error", err))
	if _, stack := extractError(err); stack != "" {
		attrs = append(attrs, slog.String("stack", stack))
	}
	return attrs
}

// Redacter defines how to log an object
type Redacter interface {
	Redact() string
}

// Server is an server logging middleware.
func Server(logger *slog.Logger) middleware.UnaryMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			startTime := time.Now()
			var kind, operation string
			if info, ok := transport.FromServerContext(ctx); ok {
				kind = info.Kind().String()
				operation = info.Operation()
			}
			reply, err = handler(ctx, req)
			level := levelForError(err)
			if !logger.Enabled(ctx, level) {
				return
			}

			attrs := make([]slog.Attr, 0, fixedAttrs+maxErrorAttrs)
			attrs = append(attrs,
				slog.String("kind", "server"),
				slog.String("component", kind),
				slog.String("operation", operation),
				slog.String("args", extractArgs(req)),
				slog.Float64("latency", time.Since(startTime).Seconds()),
			)
			attrs = append(attrs, errorAttrs(err)...)
			logger.LogAttrs(ctx, level, "server request", attrs...)
			return
		}
	}
}

// Client is a client logging middleware.
func Client(logger *slog.Logger) middleware.UnaryMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			startTime := time.Now()
			var kind, operation string
			if info, ok := transport.FromClientContext(ctx); ok {
				kind = info.Kind().String()
				operation = info.Operation()
			}
			reply, err = handler(ctx, req)
			level := levelForError(err)
			if !logger.Enabled(ctx, level) {
				return
			}

			attrs := make([]slog.Attr, 0, fixedAttrs+maxErrorAttrs)
			attrs = append(attrs,
				slog.String("kind", "client"),
				slog.String("component", kind),
				slog.String("operation", operation),
				slog.String("args", extractArgs(req)),
				slog.Float64("latency", time.Since(startTime).Seconds()),
			)
			attrs = append(attrs, errorAttrs(err)...)
			logger.LogAttrs(ctx, level, "client request", attrs...)
			return
		}
	}
}

func levelForError(err error) slog.Level {
	if err != nil {
		return slog.LevelError
	}
	return slog.LevelInfo
}

// extractArgs returns the string of the req
func extractArgs(req any) string {
	if redacter, ok := req.(Redacter); ok {
		return redacter.Redact()
	}
	if stringer, ok := req.(fmt.Stringer); ok {
		return stringer.String()
	}
	return fmt.Sprintf("%+v", req)
}

// extractError returns the level and stack to attach for err.
func extractError(err error) (slog.Level, string) {
	if err != nil {
		return levelForError(err), fmt.Sprintf("%+v", err)
	}
	return levelForError(err), ""
}
