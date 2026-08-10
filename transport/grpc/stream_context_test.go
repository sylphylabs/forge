package grpc

import (
	"context"
	"testing"

	"google.golang.org/grpc"
)

type recordingStream struct{ grpc.ServerStream }

// The stream must be readable under the same key it was stored with.
//
// An earlier version keyed the context by a struct wrapping the stream itself,
// so the value went in under one key and was looked up under another: every
// call panicked on the type assertion. Nothing caught it because the accessor
// had no callers in this repository.
func TestStreamFromServerContext(t *testing.T) {
	ss := &recordingStream{}
	ctx := context.WithValue(context.Background(), streamKey{}, grpc.ServerStream(ss))

	got, ok := StreamFromServerContext(ctx)
	if !ok {
		t.Fatal("stream not found under the key it was stored with")
	}
	if got != grpc.ServerStream(ss) {
		t.Errorf("got a different stream than was stored")
	}
}

// A miss is reported, not panicked on.
func TestStreamFromServerContextMiss(t *testing.T) {
	if _, ok := StreamFromServerContext(context.Background()); ok {
		t.Error("reported a stream in a context that has none")
	}
}
