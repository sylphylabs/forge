package middleware_test

// The examples in this file mirror the snippets in docs/agent/middleware.md
// so that the guide cannot drift from the API without breaking the build.
// When one of these stops compiling, fix the guide together with the example.

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

type tagKey struct{}

// Tagging mirrors "Writing unary middleware": the outer function body runs
// once, at wrapper construction; the returned handler runs per request.
func Tagging(value string) middleware.UnaryMiddleware {
	return func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			ctx = context.WithValue(ctx, tagKey{}, value)
			return next(ctx, req)
		}
	}
}

// callInfo mirrors the guide's transport snippet: call information comes from
// the transport context, and Operation is opaque.
func callInfo(ctx context.Context) {
	if tr, ok := transport.FromServerContext(ctx); ok {
		_ = tr.Operation() // opaque; label with it, never parse it
		_ = tr.Kind()      // transport.KindHTTP, transport.KindGRPC
		_ = tr.RequestHeader()
	}
}

func Example_unaryMiddleware() {
	handler := func(ctx context.Context, req any) (any, error) {
		callInfo(ctx)
		return ctx.Value(tagKey{}), nil
	}

	wrapped := Tagging("tagged")(handler)
	reply, err := wrapped(context.Background(), "request")
	fmt.Println(reply, err)
	// Output: tagged <nil>
}

// countingStream mirrors "Writing stream middleware": per-message behaviour
// comes from decorating ServerStream, not from a per-message hook.
type countingStream struct {
	middleware.ServerStream
	received *atomic.Int64
}

func (s *countingStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err // includes io.EOF on half-close
	}
	s.received.Add(1)
	return nil
}

func Counting(received *atomic.Int64) middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			return next(request, &countingStream{ServerStream: stream, received: received})
		}
	}
}

type nopStream struct{}

func (nopStream) Context() context.Context { return context.Background() }
func (nopStream) SendMsg(any) error        { return nil }
func (nopStream) RecvMsg(any) error        { return nil }

func Example_streamMiddleware() {
	var received atomic.Int64

	handler := func(request any, stream middleware.ServerStream) error {
		var msg any
		return stream.RecvMsg(&msg)
	}

	wrapped := Counting(&received)(handler)
	if err := wrapped(nil, nopStream{}); err != nil {
		fmt.Println(err)
	}
	fmt.Println(received.Load())
	// Output: 1
}

// Example_composingByHand mirrors "Composing by hand": only for building your
// own runtime; generated wrappers already do this.
func Example_composingByHand() {
	a := Tagging("a")
	b := Tagging("b")
	c := Tagging("c")
	next := func(ctx context.Context, req any) (any, error) { return req, nil }

	chained := middleware.ChainUnary(a, b, c)           // no validation
	handler, err := middleware.ComposeUnary(next, a, b) // validates, returns error
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = chained(next)
	_ = handler

	// ChainStream and ComposeStream are the stream equivalents.
	_ = middleware.ChainStream(Counting(new(atomic.Int64)))
	streamHandler, err := middleware.ComposeStream(
		func(request any, stream middleware.ServerStream) error { return nil },
		Counting(new(atomic.Int64)),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	_ = streamHandler

	fmt.Println("composed")
	// Output: composed
}
