package grpc

import (
	"context"
	stderrors "errors"

	"google.golang.org/grpc"
	grpcmd "google.golang.org/grpc/metadata"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/backstop"
	ic "github.com/sylphylabs/forge/internal/context"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// callKey carries the terminal handlers of one RPC through the server-wide
// middleware chain. The chain itself is composed once, in NewServer; only the
// grpc-go continuation of the current call travels through the context.
type callKey struct{}

type call struct {
	unary  middleware.UnaryHandler
	stream middleware.StreamHandler
}

// errLostCall reports server-wide middleware that severed the call from its
// continuation by passing next a context not derived from the one it was
// given.
var errLostCall = errors.MustDefine(errors.KindInternal, errors.Domain, "GRPC_DISPATCH").
	Msg("server middleware severed the call from its handler")

// callFrom returns the current call's terminal handlers. The interceptor is
// the only writer of the key, so a miss cannot happen on a served request; the
// nil-map-safe zero value keeps the accessor total anyway.
func callFrom(ctx context.Context) call {
	c, _ := ctx.Value(callKey{}).(call)
	return c
}

// unaryServerInterceptor is a gRPC unary server interceptor
func (s *Server) unaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (reply any, err error) {
		// The backstop is the transport's own recover, outside even
		// server-wide middleware: a panic anywhere below is logged with its
		// stack and leaves the process as a generic internal error.
		defer func() {
			if rec := recover(); rec != nil {
				reply, err = nil, s.projectError(backstop.Recovered(ctx, "[gRPC]", rec))
			}
		}()
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
		ctx = context.WithValue(ctx, callKey{}, call{unary: func(ctx context.Context, req any) (any, error) {
			return handler(ctx, req)
		}})
		reply, err = s.unaryHandler(ctx, req)
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

// streamServerInterceptor is a gRPC stream server interceptor
func (s *Server) streamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		// The backstop is the transport's own recover, outside even
		// server-wide middleware: a panic anywhere below is logged with its
		// stack and leaves the process as a generic internal error.
		defer func() {
			if rec := recover(); rec != nil {
				err = s.projectError(backstop.Recovered(ss.Context(), "[gRPC]", rec))
			}
		}()
		ctx, cancel := ic.Merge(ss.Context(), s.baseCtx)
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

		ctx = context.WithValue(ctx, streamKey{}, ss)
		ctx = context.WithValue(ctx, callKey{}, call{stream: func(_ any, mws middleware.ServerStream) error {
			// Present the possibly decorated stream to grpc-go's method
			// handler; transport-only capabilities stay on the native stream.
			return handler(srv, &chainedStream{ServerStream: ss, mws: mws})
		}})

		err = s.streamHandler(nil, &grpcServerStream{ServerStream: ss, ctx: ctx})
		if len(replyHeader) > 0 {
			_ = grpc.SetHeader(ctx, replyHeader)
		}
		return s.projectError(err)
	}
}

// grpcServerStream is the middleware view of a native stream: the interceptor
// context replaces the native one, message flow stays native.
type grpcServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *grpcServerStream) Context() context.Context { return s.ctx }

// chainedStream is the native view of a middleware-decorated stream: grpc-go's
// method handler sees the decorated context and message flow, while every
// transport-only capability (headers, trailers, peer) stays on the embedded
// native stream.
type chainedStream struct {
	grpc.ServerStream
	mws middleware.ServerStream
}

func (s *chainedStream) Context() context.Context { return s.mws.Context() }
func (s *chainedStream) SendMsg(m any) error      { return s.mws.SendMsg(m) }
func (s *chainedStream) RecvMsg(m any) error      { return s.mws.RecvMsg(m) }

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
