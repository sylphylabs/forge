package logging

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/openkratos/kratos/errors"
	"github.com/openkratos/kratos/middleware"
	"github.com/openkratos/kratos/transport"
	"github.com/openkratos/kratos/transport/http/status"
)

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

			code := int32(status.FromGRPCCode(codes.OK))
			var reason string
			if se := errors.FromError(err); se != nil {
				code = se.Code
				reason = se.Reason
			}
			_, stack := extractError(err)
			attrs := []slog.Attr{
				slog.String("kind", "server"),
				slog.String("component", kind),
				slog.String("operation", operation),
				slog.String("args", extractArgs(req)),
				slog.Int64("code", int64(code)),
				slog.String("reason", reason),
				slog.Float64("latency", time.Since(startTime).Seconds()),
			}
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
				if stack != "" {
					attrs = append(attrs, slog.String("stack", stack))
				}
			}
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

			code := int32(status.FromGRPCCode(codes.OK))
			var reason string
			if se := errors.FromError(err); se != nil {
				code = se.Code
				reason = se.Reason
			}
			_, stack := extractError(err)
			attrs := []slog.Attr{
				slog.String("kind", "client"),
				slog.String("component", kind),
				slog.String("operation", operation),
				slog.String("args", extractArgs(req)),
				slog.Int64("code", int64(code)),
				slog.String("reason", reason),
				slog.Float64("latency", time.Since(startTime).Seconds()),
			}
			if err != nil {
				attrs = append(attrs, slog.Any("error", err))
				if stack != "" {
					attrs = append(attrs, slog.String("stack", stack))
				}
			}
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
