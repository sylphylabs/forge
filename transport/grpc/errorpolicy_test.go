package grpc

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/sylphylabs/forge/errors"
)

// Only the error's public data reaches the wire; a cause stays local.
func TestOutgoingErrorDisclosesOnlyPublicData(t *testing.T) {
	srv := NewServer()
	secret := errors.Of(errors.KindInternal).WithReason("DB_DOWN").
		Msg("lookup failed").
		Wrap(stderrors.New("dial tcp 10.0.0.1:5432: password=hunter2"))

	out := srv.projectError(secret)
	gs, ok := status.FromError(out)
	if !ok {
		t.Fatal("grpc-go did not recognize the outgoing error")
	}
	if strings.Contains(gs.Message(), "hunter2") {
		t.Errorf("the cause reached the wire: %q", gs.Message())
	}
	if gs.Message() != "lookup failed" {
		t.Errorf("message = %q, want the declared one", gs.Message())
	}
}

func TestNilErrorPassesThrough(t *testing.T) {
	srv := NewServer()
	interceptor := srv.unaryServerInterceptor()
	if _, err := interceptor(t.Context(), nil, &grpc.UnaryServerInfo{FullMethod: "/x/Y"},
		func(context.Context, any) (any, error) { return "ok", nil }); err != nil {
		t.Errorf("err = %v, want nil", err)
	}
}
