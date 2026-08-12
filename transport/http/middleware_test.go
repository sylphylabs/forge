package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sylphylabs/forge/middleware"
)

// The backstop must fire with zero middleware configured: it is the
// transport's guarantee, not an optional layer.
func TestBackstopRecoversPanic(t *testing.T) {
	srv := NewServer()
	srv.Route("/").GET("/panic", func(Context) error {
		panic("secret panic detail")
	})
	srv.Route("/").GET("/ok", func(ctx Context) error {
		return ctx.String(http.StatusOK, "ok")
	})

	res := httptest.NewRecorder()
	srv.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	body := res.Body.String()
	if strings.Contains(body, "secret panic detail") {
		t.Errorf("response %q discloses the panic value", body)
	}
	var problem struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &problem); err != nil {
		t.Fatalf("response %q is not a problem document: %v", body, err)
	}
	if problem.Kind != "INTERNAL" {
		t.Errorf("kind = %q, want INTERNAL", problem.Kind)
	}

	// The process survived: the same server answers the next request.
	res = httptest.NewRecorder()
	srv.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status after panic = %d, want %d", res.Code, http.StatusOK)
	}
}

// The backstop sits outside server-wide middleware, so a panic raised by the
// middleware itself is contained too.
func TestBackstopContainsPanickingServerMiddleware(t *testing.T) {
	srv := NewServer(WithMiddleware(func(middleware.UnaryHandler) middleware.UnaryHandler {
		return func(context.Context, any) (any, error) {
			panic("middleware panic")
		}
	}))
	srv.Route("/").GET("/x", func(ctx Context) error {
		return ctx.String(http.StatusOK, "ok")
	})

	res := httptest.NewRecorder()
	srv.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", res.Code, http.StatusInternalServerError)
	}
	if body := res.Body.String(); strings.Contains(body, "middleware panic") {
		t.Errorf("response %q discloses the panic value", body)
	}
}

// http.ErrAbortHandler is net/http's own control flow and must keep
// propagating; the response is already unrecoverable when it is raised.
func TestBackstopRepanicsAbortHandler(t *testing.T) {
	srv := NewServer()
	srv.Route("/").GET("/abort", func(Context) error {
		panic(http.ErrAbortHandler)
	})

	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler { //nolint:errorlint // identity is the contract
			t.Fatalf("recovered %v, want http.ErrAbortHandler", rec)
		}
	}()
	srv.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/abort", nil))
	t.Fatal("ErrAbortHandler must propagate to net/http")
}

// Server-wide middleware sees the routed operation and returns an error
// through the server's error encoder.
func TestServerMiddlewareShortCircuits(t *testing.T) {
	var order []string
	var mu sync.Mutex
	add := func(name string) {
		mu.Lock()
		order = append(order, name)
		mu.Unlock()
	}
	srv := NewServer(WithMiddleware(func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			add("server-wide")
			if r, ok := req.(*http.Request); ok && r.URL.Query().Get("deny") == "1" {
				return nil, errLostDispatch.Msg("denied")
			}
			return next(ctx, req)
		}
	}))
	srv.Route("/").GET("/x", func(ctx Context) error {
		add("handler")
		return ctx.String(http.StatusOK, "ok")
	})

	res := httptest.NewRecorder()
	srv.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	srv.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x?deny=1", nil))
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("denied status = %d", res.Code)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"server-wide", "handler", "server-wide"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// A nil middleware fails composition inside NewServer; Start and Endpoint
// report it, the way they report a bad listener.
func TestNewServerReportsBadMiddleware(t *testing.T) {
	srv := NewServer(WithMiddleware(nil))
	if err := srv.Start(context.Background()); err == nil {
		t.Error("Start with a nil middleware must fail")
	}
	if _, err := srv.Endpoint(); err == nil {
		t.Error("Endpoint with a nil middleware must fail")
	}
}
