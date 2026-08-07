package logging

import (
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
	"github.com/sylphylabs/forge/transport/http/status"
)

// ServerStream is a server logging middleware for streaming methods.
//
// One record is emitted per stream, after the stream completes, with the
// latency of the whole stream lifecycle. Individual messages are not logged:
// a long-lived stream can carry an unbounded number of them, and logging
// payloads per message would both flood the log and widen the exposure of
// user data. Use a transport or application middleware if per-message
// records are required.
//
// The args attribute holds the initial request for server-streaming methods
// and is empty for client and bidirectional streaming methods, where no
// initial request exists.
func ServerStream(logger *slog.Logger) middleware.StreamMiddleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			startTime := time.Now()
			ctx := stream.Context()
			var kind, operation string
			if info, ok := transport.FromServerContext(ctx); ok {
				kind = info.Kind().String()
				operation = info.Operation()
			}
			err := handler(request, stream)
			level := levelForError(err)
			if !logger.Enabled(ctx, level) {
				return err
			}

			code := int32(status.FromGRPCCode(codes.OK))
			var reason string
			if se := errors.FromError(err); se != nil {
				code = se.Code
				reason = se.Reason
			}
			var args string
			if request != nil {
				args = extractArgs(request)
			}
			_, stack := extractError(err)
			attrs := []slog.Attr{
				slog.String("kind", "server"),
				slog.String("component", kind),
				slog.String("operation", operation),
				slog.String("args", args),
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
			logger.LogAttrs(ctx, level, "server stream", attrs...)
			return err
		}
	}
}
