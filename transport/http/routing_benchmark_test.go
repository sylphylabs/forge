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
	value  string
}

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
