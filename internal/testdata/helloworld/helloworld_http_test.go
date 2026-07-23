package helloworld

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/openkratos/kratos/middleware"
	kratoshttp "github.com/openkratos/kratos/transport/http"
)

type middlewareContextKey struct{}

type middlewareGreeter struct {
	t *testing.T
}

func (g middlewareGreeter) SayHello(ctx context.Context, req *HelloRequest) (*HelloReply, error) {
	g.t.Helper()
	if got := ctx.Value(middlewareContextKey{}); got != "middleware" {
		g.t.Fatalf("middleware context value = %v", got)
	}
	return &HelloReply{Message: req.GetName()}, nil
}

func TestGeneratedUnaryMiddlewareIsPrecomposed(t *testing.T) {
	var compositions atomic.Int32
	var calls atomic.Int32
	m := func(next middleware.UnaryHandler) middleware.UnaryHandler {
		compositions.Add(1)
		return func(ctx context.Context, req any) (any, error) {
			calls.Add(1)
			ctx = context.WithValue(ctx, middlewareContextKey{}, "middleware")
			return next(ctx, req)
		}
	}

	srv := kratoshttp.NewServer(kratoshttp.Timeout(0))
	srv.Use(OperationGreeterSayHello, m)
	RegisterGreeterHTTPServer(srv, middlewareGreeter{t: t})
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/helloworld/openkratos", nil)
		srv.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
		}
	}

	if got := compositions.Load(); got != 1 {
		t.Fatalf("middleware compositions = %d, want 1", got)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("middleware calls = %d, want 2", got)
	}
}
