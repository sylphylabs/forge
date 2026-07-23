package middleware

import (
	"context"
	"fmt"
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

// ComposeUnary validates and composes a unary handler during wrapper
// construction. It is intended for generated registration-time wiring.
func ComposeUnary(next UnaryHandler, m ...UnaryMiddleware) (UnaryHandler, error) {
	if next == nil {
		return nil, fmt.Errorf("middleware: nil unary handler")
	}
	for i := len(m) - 1; i >= 0; i-- {
		if m[i] == nil {
			return nil, fmt.Errorf("middleware: nil unary middleware at index %d", i)
		}
		next = m[i](next)
		if next == nil {
			return nil, fmt.Errorf("middleware: unary middleware at index %d returned a nil handler", i)
		}
	}
	return next, nil
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

// ComposeStream validates and composes a stream handler during wrapper
// construction. It is intended for generated registration-time wiring.
func ComposeStream(next StreamHandler, m ...StreamMiddleware) (StreamHandler, error) {
	if next == nil {
		return nil, fmt.Errorf("middleware: nil stream handler")
	}
	for i := len(m) - 1; i >= 0; i-- {
		if m[i] == nil {
			return nil, fmt.Errorf("middleware: nil stream middleware at index %d", i)
		}
		next = m[i](next)
		if next == nil {
			return nil, fmt.Errorf("middleware: stream middleware at index %d returned a nil handler", i)
		}
	}
	return next, nil
}
