package recovery

import (
	"context"
	"runtime"
	"time"

	"log/slog"

	"github.com/sylphylabs/forge/middleware"
)

// RecoveryStream is a server middleware that recovers from any panics raised
// during a stream lifecycle.
//
// The handler argument passed to HandlerFunc is the initial request for
// server-streaming methods and nil for client and bidirectional streaming
// methods, matching [middleware.StreamHandler].
func RecoveryStream(opts ...Option) middleware.StreamMiddleware {
	op := options{
		handler: func(context.Context, any, any) error {
			return ErrUnknownRequest
		},
	}
	for _, o := range opts {
		o(&op)
	}
	logger := op.logger
	if logger == nil {
		logger = slog.Default()
	}
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) (err error) {
			startTime := time.Now()
			defer func() {
				if rerr := recover(); rerr != nil {
					buf := make([]byte, 64<<10) //nolint:mnd
					n := runtime.Stack(buf, false)
					buf = buf[:n]
					ctx := stream.Context()
					logger.ErrorContext(ctx, "panic recovered",
						slog.Any("panic", rerr),
						slog.Any("request", request),
						slog.String("stack", string(buf)),
					)
					ctx = context.WithValue(ctx, Latency{}, time.Since(startTime).Seconds())
					err = op.handler(ctx, request, rerr)
				}
			}()
			return handler(request, stream)
		}
	}
}
