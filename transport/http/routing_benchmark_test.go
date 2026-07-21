package http

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type benchmarkResponseWriter struct {
	header http.Header
}

var benchmarkValue string

func (w *benchmarkResponseWriter) Header() http.Header       { return w.header }
func (*benchmarkResponseWriter) Write(p []byte) (int, error) { return io.Discard.Write(p) }
func (*benchmarkResponseWriter) WriteHeader(int)             {}

func BenchmarkRouteMux(b *testing.B) {
	for _, routes := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("static/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/static/%d", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/static/%d", routes-1))
		})
		b.Run(fmt.Sprintf("parameter/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(benchmarkHandler), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1))
		})
		b.Run(fmt.Sprintf("parameter-vars/%d", routes), func(b *testing.B) {
			router := newRouteMux()
			for i := range routes {
				router.handle(http.MethodGet, fmt.Sprintf("/resource/%d/{id}", i), http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
					benchmarkValue = requestVars(req).Get("id")
				}), false)
			}
			benchmarkRouter(b, router, fmt.Sprintf("/resource/%d/42", routes-1))
		})
	}
}

func benchmarkHandler(http.ResponseWriter, *http.Request) {}

func benchmarkRouter(b *testing.B, handler http.Handler, path string) {
	b.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := &benchmarkResponseWriter{header: make(http.Header)}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		handler.ServeHTTP(w, req)
	}
}
