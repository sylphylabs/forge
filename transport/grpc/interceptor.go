package grpc

import (
	"context"
	stderrors "errors"

	"google.golang.org/grpc"
	grpcmd "google.golang.org/grpc/metadata"

	"github.com/sylphylabs/forge/errors"
	ic "github.com/sylphylabs/forge/internal/context"
	"github.com/sylphylabs/forge/transport"
)

// unaryServerInterceptor is a gRPC unary server interceptor
func (s *Server) unaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, cancel := ic.Merge(ctx, s.baseCtx)
		defer cancel()
		md, _ := grpcmd.FromIncomingContext(ctx)
		replyHeader := grpcmd.MD{}
		tr := &Transport{
			operation:   info.FullMethod,
			reqHeader:   headerCarrier(md),
			replyHeader: headerCarrier(replyHeader),
		}
		if s.endpoint != nil {
			tr.endpoint = s.endpoint.String()
		}
		ctx = transport.NewServerContext(ctx, tr)
		if s.timeout > 0 {
			ctx, cancel = context.WithTimeout(ctx, s.timeout)
			defer cancel()
		}
		reply, err := handler(ctx, req)
		if len(replyHeader) > 0 {
			_ = grpc.SetHeader(ctx, replyHeader)
		}
		return reply, s.projectError(err)
	}
}

// projectError prepares an outgoing error for the wire.
//
// It attaches the gRPC status at the last point the server owns the value, so
// grpc-go carries the error's kind, reason, and details rather than reporting a
// bare Unknown — the errors package holds no gRPC types, so nothing before this
// point could have done so. Only the error's public data reaches the status; a
// cause never leaves the process.
func (s *Server) projectError(err error) error {
	if err == nil {
		return nil
	}
	forge := forgeError(err)
	return &statusError{err: forge, status: StatusFrom(errors.PublicOf(forge))}
}

// forgeError converts any handler error into a Forge error.
//
// A handler may return a plain gRPC status — from grpc-go itself, from a
// generated stub, or by calling status.Error directly. Such an error already
// names its code, so it is translated rather than classified as unknown, which
// would both lose the code and trip the policy into redacting a message the
// handler chose deliberately.
func forgeError(err error) *errors.Error {
	var forge *errors.Error
	if stderrors.As(err, &forge) {
		return errors.FromError(err)
	}
	if converted, ok := ErrorFrom(err); ok {
		return converted
	}
	return errors.FromError(err)
}

// wrappedStream is rewrite grpc stream's context
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func NewWrappedStream(ctx context.Context, stream grpc.ServerStream) grpc.ServerStream {
	return &wrappedStream{
		ServerStream: stream,
		ctx:          ctx,
	}
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}

// streamServerInterceptor is a gRPC stream server interceptor
func (s *Server) streamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, cancel := ic.Merge(ss.Context(), s.baseCtx)
		defer cancel()
		md, _ := grpcmd.FromIncomingContext(ctx)
		replyHeader := grpcmd.MD{}
		ctx = transport.NewServerContext(ctx, &Transport{
			endpoint:    s.endpoint.String(),
			operation:   info.FullMethod,
			reqHeader:   headerCarrier(md),
			replyHeader: headerCarrier(replyHeader),
		})

		ctx = context.WithValue(ctx, streamKey{}, ss)
		ws := NewWrappedStream(ctx, ss)

		err := handler(srv, ws)
		if len(replyHeader) > 0 {
			_ = grpc.SetHeader(ctx, replyHeader)
		}
		return s.projectError(err)
	}
}

// streamKey is the context key carrying the server stream.
//
// It is an empty struct so that storing and reading use the same key. An
// earlier version keyed by a struct wrapping the stream itself, which meant the
// value was stored under one key and looked up under another, and every call
// panicked on the type assertion.
type streamKey struct{}

// StreamFromServerContext returns the server stream ctx was created for.
//
// It reports whether one was present, matching the other From*Context
// accessors in this repository, so a caller handles a miss rather than
// panicking on it.
func StreamFromServerContext(ctx context.Context) (grpc.ServerStream, bool) {
	ss, ok := ctx.Value(streamKey{}).(grpc.ServerStream)
	return ss, ok
}
