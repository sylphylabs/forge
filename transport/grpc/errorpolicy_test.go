package grpc

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sylphylabs/forge/errors"
)

// errDown is a declared identity, so its public data may cross the boundary.
var errDown = errors.MustDefine(errors.KindInternal, "test.v1", "DB_DOWN")

// Only the error's public data reaches the wire; a cause stays local.
func TestOutgoingErrorDisclosesOnlyPublicData(t *testing.T) {
	srv := NewServer()
	secret := errDown.
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

// An identity that was never declared is not part of any contract, so nothing
// it carries may leave the process: it projects as a bare internal failure and
// its ad-hoc reason, message, and metadata stay in the logs.
func TestOutgoingUndeclaredErrorProjectsInternal(t *testing.T) {
	srv := NewServer()
	adHoc := errors.Of(errors.KindNotFound).
		WithDomain("store.internal").
		WithReason("DEVICE_NOT_FOUND").
		Msg("device row missing").
		Meta("device_id", "dev-1")

	out := srv.projectError(adHoc)
	gs, ok := status.FromError(out)
	if !ok {
		t.Fatal("grpc-go did not recognize the outgoing error")
	}
	if gs.Code() != codes.Internal {
		t.Errorf("code = %v, want Internal for an undeclared identity", gs.Code())
	}
	if gs.Message() != "" {
		t.Errorf("message = %q, want it withheld", gs.Message())
	}
	for _, detail := range gs.Details() {
		if info, isInfo := detail.(*errdetails.ErrorInfo); isInfo {
			if info.GetReason() != "" || info.GetDomain() != "" || len(info.GetMetadata()) > 0 {
				t.Errorf("undeclared identity reached the wire: %v", info)
			}
		}
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
