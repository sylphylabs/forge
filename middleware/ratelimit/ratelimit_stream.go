package ratelimit

import (
	"github.com/sylphylabs/forge/middleware"
)

// ServerStream limits how often a stream may be opened. One token is taken
// when the stream starts and released when it ends, so the limiter observes
// the lifetime of the whole stream.
//
// This is the right choice when the cost being protected is the stream itself
// — a connection, a worker, a session. Use [PerMessageServerStream] instead
// when the cost is per message.
func ServerStream(opts ...Option) middleware.StreamMiddleware {
	options := newOptions(opts...)
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			done, err := options.limiter.Allow()
			if err != nil {
				return ErrLimitExceed
			}
			err = handler(request, stream)
			done(DoneInfo{Err: err})
			return err
		}
	}
}

// PerMessageServerStream limits how often a message may be received on a
// stream. One token is taken per successful RecvMsg; sends are not limited.
//
// This is the right choice when the cost being protected scales with message
// volume. A rejected message fails that RecvMsg with [ErrLimitExceed] and
// leaves the stream open, so the handler decides whether to continue or
// return. Use [ServerStream] instead to limit how often streams open.
func PerMessageServerStream(opts ...Option) middleware.StreamMiddleware {
	options := newOptions(opts...)
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			return handler(request, &limitedStream{
				ServerStream: stream,
				limiter:      options.limiter,
			})
		}
	}
}

func newOptions(opts ...Option) *options {
	options := &options{
		limiter: newDefaultLimiter(),
	}
	for _, o := range opts {
		o(options)
	}
	return options
}

// limitedStream applies the limiter to every received message.
type limitedStream struct {
	middleware.ServerStream
	limiter Limiter
}

func (s *limitedStream) RecvMsg(m any) error {
	done, err := s.limiter.Allow()
	if err != nil {
		return ErrLimitExceed
	}
	err = s.ServerStream.RecvMsg(m)
	done(DoneInfo{Err: err})
	return err
}
