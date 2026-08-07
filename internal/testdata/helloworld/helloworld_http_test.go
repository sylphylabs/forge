package helloworld

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/sylphylabs/forge/middleware"
	forgehttp "github.com/sylphylabs/forge/transport/http"
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

	srv := forgehttp.NewServer(forgehttp.Timeout(0))
	service, err := WrapGreeterHTTPServer(middlewareGreeter{t: t}, GreeterMiddleware{
		Methods: GreeterMethodMiddleware{SayHello: []middleware.UnaryMiddleware{m}},
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterGreeterHTTPServer(srv, service)
	for range 2 {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/helloworld/forge", nil)
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
