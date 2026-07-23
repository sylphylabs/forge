package http

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/openkratos/kratos/internal/testdata/binding"
	"github.com/openkratos/kratos/middleware"
)

type benchmarkResponseWriter struct {
	header http.Header
	value  string
}

func (w *benchmarkResponseWriter) Header() http.Header       { return w.header }
func (*benchmarkResponseWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (*benchmarkResponseWriter) WriteHeader(int)             {}

func BenchmarkRouteMux(b *testing.B) {
	b.Run("header-static", func(b *testing.B) {
		router := newRouteMux()
		router.handleHeader("X-Route", "special", http.HandlerFunc(benchmarkHandler))
		router.handle(http.MethodGet, "/static", http.HandlerFunc(benchmarkHandler), false)
		benchmarkRouter(b, router, "/static", false)
	})
	b.Run("header-match", func(b *testing.B) {
		router := newRouteMux()
		router.handleHeader("X-Route", "special", http.HandlerFunc(benchmarkHandler))
		benchmarkRouterWithHeader(b, router, "/static", "X-Route", "special")
	})
	b.Run("custom-not-found", func(b *testing.B) {
		router := newRouteMux()
		router.notFoundHandler = http.HandlerFunc(benchmarkHandler)
		router.handle(http.MethodGet, "/resource", http.HandlerFunc(benchmarkHandler), false)
		benchmarkRouter(b, router, "/missing", false)
	})
	b.Run("custom-method-not-allowed", func(b *testing.B) {
		router := newRouteMux()
		router.methodNotAllowedHandler = http.HandlerFunc(benchmarkHandler)
		router.handle(http.MethodGet, "/resource", http.HandlerFunc(benchmarkHandler), false)
		benchmarkRouterMethod(b, router, http.MethodPost, "/resource")
	})
	for _, routes := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("static/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/static/%d", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/static/%d", routes-1), false)
		})
		b.Run(fmt.Sprintf("static-parallel/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/static/%d", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/static/%d", routes-1), true)
		})
		b.Run(fmt.Sprintf("parameter/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), false)
		})
		b.Run(fmt.Sprintf("parameter-parallel/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), true)
		})
		b.Run(fmt.Sprintf("parameter-vars/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.(*benchmarkResponseWriter).value = requestVars(req).Get("id")
				}), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), false)
		})
		b.Run(fmt.Sprintf("parameter-proto/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{value}", i), http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					var target wrapperspb.StringValue
					if err := DefaultRequestVars(req, &target); err != nil {
						b.Fatal(err)
					}
				}), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), false)
		})
		b.Run(fmt.Sprintf("parameter-proto-values/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{value}", i), http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					var target wrapperspb.StringValue
					if err := bindQuery(requestVars(req), &target); err != nil {
						b.Fatal(err)
					}
				}), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), false)
		})
		b.Run(fmt.Sprintf("parameter-vars-parallel/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.(*benchmarkResponseWriter).value = requestVars(req).Get("id")
				}), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1), true)
		})
		b.Run(fmt.Sprintf("miss/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/static/%d", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, "/missing", false)
		})
		b.Run(fmt.Sprintf("miss-parallel/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/static/%d", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, "/missing", true)
		})
	}
}

func BenchmarkServerRoute(b *testing.B) {
	b.Run("parameter", func(b *testing.B) {
		srv := NewServer(Timeout(0))
		srv.Route("").GET("/resource/{value}", func(Context) error { return nil })
		benchmarkRouter(b, srv, "/resource/42", false)
	})
	b.Run("parameter-vars", func(b *testing.B) {
		srv := NewServer(Timeout(0))
		srv.Route("").GET("/resource/{value}", func(ctx Context) error {
			ctx.Response().(*benchmarkResponseWriter).value = ctx.Vars().Get("value")
			return nil
		})
		benchmarkRouter(b, srv, "/resource/42", false)
	})
	b.Run("parameter-proto", func(b *testing.B) {
		srv := NewServer(Timeout(0))
		srv.Route("").GET("/resource/{value}", func(ctx Context) error {
			var target wrapperspb.StringValue
			return ctx.BindVars(&target)
		})
		benchmarkRouter(b, srv, "/resource/42", false)
	})
	b.Run("parameter-proto-nested", func(b *testing.B) {
		srv := NewServer(Timeout(0))
		srv.Route("").GET("/resource/{sub.naming}", func(ctx Context) error {
			var target binding.HelloRequest
			return ctx.BindVars(&target)
		})
		benchmarkRouter(b, srv, "/resource/42", false)
	})
	b.Run("parameter-aip", func(b *testing.B) {
		srv := NewServer(Timeout(0))
		srv.Route("").GET("/v1/{name=publishers/*/books/*}", func(ctx Context) error {
			ctx.Response().(*benchmarkResponseWriter).value = ctx.Vars().Get("name")
			return nil
		})
		benchmarkRouter(b, srv, "/v1/publishers/acme/books/42", false)
	})
}

func BenchmarkMiddlewareDispatch(b *testing.B) {
	operation := "/benchmark.Service/Call"
	terminal := func(context.Context, any) (any, error) { return nil, nil }
	for _, count := range []int{0, 1, 3} {
		b.Run(fmt.Sprintf("dynamic/%d", count), func(b *testing.B) {
			middlewares := make([]middleware.Middleware, count)
			for i := range middlewares {
				middlewares[i] = func(next middleware.Handler) middleware.Handler { return next }
			}
			srv := NewServer(Middleware(middlewares...))
			ctx := &wrapper{
				router: srv.Route("/"),
				req:    httptest.NewRequest(http.MethodGet, operation, nil),
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := ctx.Middleware(terminal)(ctx, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("precomposed/%d", count), func(b *testing.B) {
			middlewares := make([]middleware.Middleware, count)
			for i := range middlewares {
				middlewares[i] = func(next middleware.Handler) middleware.Handler { return next }
			}
			srv := NewServer(Middleware(middlewares...))
			handler := srv.WrapMiddleware(operation, terminal)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := handler(context.Background(), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkRouterWithHeader(b *testing.B, handler http.Handler, path, key, value string) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set(key, value)
	w := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		handler.ServeHTTP(w, req)
	}
}

func benchmarkRouterMethod(b *testing.B, handler http.Handler, method, path string) {
	b.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		handler.ServeHTTP(w, req)
	}
}

func benchmarkHandler(http.ResponseWriter, *http.Request) {}

func benchmarkRouter(b *testing.B, handler http.Handler, path string, parallel bool) {
	b.Helper()
	if parallel {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			w := &benchmarkResponseWriter{header: make(http.Header)}
			for pb.Next() {
				handler.ServeHTTP(w, req)
			}
		})
		return
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		handler.ServeHTTP(w, req)
	}
}
