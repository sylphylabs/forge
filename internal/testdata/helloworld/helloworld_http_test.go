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

	srv := forgehttp.NewServer(forgehttp.WithTimeout(0))
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

type orderGreeter struct{ order *[]string }

func (g orderGreeter) SayHello(_ context.Context, req *HelloRequest) (*HelloReply, error) {
	*g.order = append(*g.order, "handler")
	return &HelloReply{Message: req.GetName()}, nil
}

// Server-wide middleware runs outside (before) the generated per-service plan.
func TestServerWideMiddlewareRunsOutsideGeneratedPlan(t *testing.T) {
	var order []string
	record := func(name string) middleware.UnaryMiddleware {
		return func(next middleware.UnaryHandler) middleware.UnaryHandler {
			return func(ctx context.Context, req any) (any, error) {
				order = append(order, name)
				return next(ctx, req)
			}
		}
	}

	srv := forgehttp.NewServer(forgehttp.WithTimeout(0), forgehttp.WithMiddleware(record("server-wide")))
	service, err := WrapGreeterHTTPServer(orderGreeter{order: &order}, GreeterMiddleware{
		Unary: []middleware.UnaryMiddleware{record("generated")},
	})
	if err != nil {
		t.Fatal(err)
	}
	RegisterGreeterHTTPServer(srv, service)

	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/helloworld/forge", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
	want := []string{"server-wide", "generated", "handler"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}
