package middleware

import (
	"context"
)

// UnaryHandler handles one request and returns one reply.
type UnaryHandler func(ctx context.Context, req any) (any, error)

// UnaryMiddleware wraps a UnaryHandler.
type UnaryMiddleware func(UnaryHandler) UnaryHandler

// ChainUnary composes unary middleware in declaration order. The first
// middleware is the outermost wrapper and runs first on entry.
func ChainUnary(m ...UnaryMiddleware) UnaryMiddleware {
	return func(next UnaryHandler) UnaryHandler {
		for i := len(m) - 1; i >= 0; i-- {
			next = m[i](next)
		}
		return next
	}
}

// ServerStream is the transport-neutral server stream surface available to
// middleware. Transport-specific capabilities remain on their native stream.
type ServerStream interface {
	Context() context.Context
	SendMsg(any) error
	RecvMsg(any) error
}

// StreamHandler handles one complete server stream lifecycle. Request is the
// decoded initial request for server-streaming methods and nil for client and
// bidirectional streaming methods.
type StreamHandler func(request any, stream ServerStream) error

// StreamMiddleware wraps a complete server stream lifecycle.
type StreamMiddleware func(StreamHandler) StreamHandler

// ChainStream composes stream middleware in declaration order. The first
// middleware is the outermost wrapper and runs first on entry.
func ChainStream(m ...StreamMiddleware) StreamMiddleware {
	return func(next StreamHandler) StreamHandler {
		for i := len(m) - 1; i >= 0; i-- {
			next = m[i](next)
		}
		return next
	}
}
