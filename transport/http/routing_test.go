package http

import (
	stderrors "errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	kratoserrors "github.com/openkratos/kratos/errors"
)

func TestCompileRoute(t *testing.T) {
	tests := []struct {
		template string
		pattern  string
	}{
		{template: "/", pattern: "/{$}"},
		{template: "/users/{id}", pattern: "/users/{__openkratos0}"},
		{
			template: "/v1/{message.name=publishers/*/books/*}",
			pattern:  "/v1/publishers/{__openkratos0}/books/{__openkratos1}",
		},
		{template: "/files/{path...}", pattern: "/files/{__openkratos0...}"},
		{template: "/v1/{name}:archive", pattern: "/v1/{__openkratos0}"},
		{template: "/items/{id:[0-9]+}", pattern: "/items/{__openkratos0}"},
		{template: "/items/{id:[^/]+}", pattern: "/items/{__openkratos0}"},
	}
	for _, tt := range tests {
		t.Run(tt.template, func(t *testing.T) {
			got, err := compileRoute(tt.template)
			if err != nil {
				t.Fatal(err)
			}
			if got.pattern != tt.pattern {
				t.Fatalf("pattern = %q, want %q", got.pattern, tt.pattern)
			}
		})
	}
}

func TestCompileRouteRejectsMultiSegmentLegacyRegex(t *testing.T) {
	if _, err := compileRoute("/v1/{name:publishers/[^/]+}"); err == nil {
		t.Fatal("expected a multi-segment legacy regular expression error")
	}
}

func TestCompileRouteRejectsReservedVariableName(t *testing.T) {
	if _, err := compileRoute("/v1/{__openkratos0}"); err == nil {
		t.Fatal("expected a reserved path variable name error")
	}
}

func TestCompileRouteRejectsMiddleMultiWildcard(t *testing.T) {
	if _, err := compileRoute("/v1/{name=**}/sub"); err == nil {
		t.Fatal("expected a non-terminal multi-segment wildcard error")
	}
}

func TestRouteMuxAIPVariables(t *testing.T) {
	srv := NewServer()
	srv.Route("/").GET("/v1/{message.name=publishers/*/books/*}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("message.name"))
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/publishers/acme/books/42", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if got := w.Body.String(); got != "publishers/acme/books/42" {
		t.Fatalf("body = %q", got)
	}
}

func TestRouteMuxGoogleEscapedVariables(t *testing.T) {
	srv := NewServer()
	route := srv.Route("/")
	route.GET("/single/{name}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("name")+"|"+ctx.Request().PathValue("name"))
	})
	route.GET("/multi/{name=**}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("name")+"|"+ctx.Request().PathValue("name"))
	})

	tests := []struct {
		path string
		want string
	}{
		{path: "/single/a%2Fb", want: "a/b|a/b"},
		{path: "/multi/a%2Fb/c%3Fd", want: "a%2Fb/c?d|a%2Fb/c?d"},
		{path: "/multi/a%2fb/c", want: "a%2fb/c|a%2fb/c"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			srv.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := response.Body.String(); got != tt.want {
				t.Fatalf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRouteMuxTranscodingErrorsUseConfiguredEncoder(t *testing.T) {
	var encoded error
	srv := NewServer(ErrorEncoder(func(w http.ResponseWriter, _ *http.Request, err error) {
		encoded = err
		w.WriteHeader(kratoserrors.Code(err))
	}))
	tests := []struct {
		name string
		err  error
		code int
	}{
		{name: "malformed escape", err: url.EscapeError("%ZZ"), code: http.StatusBadRequest},
		{name: "layout invariant", err: stderrors.New("layout mismatch"), code: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded = nil
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			srv.router.serveRouteError(response, request, tt.err)
			if encoded == nil {
				t.Fatal("configured error encoder was not called")
			}
			if got := kratoserrors.Code(encoded); got != tt.code {
				t.Fatalf("code = %d, want %d", got, tt.code)
			}
		})
	}
}

func TestRouteMuxConstraintVariants(t *testing.T) {
	srv := NewServer()
	route := srv.Route("/")
	route.GET("/items/{id:[0-9]+}", func(ctx Context) error {
		return ctx.String(http.StatusOK, "id:"+ctx.Vars().Get("id"))
	})
	route.GET("/items/{slug}", func(ctx Context) error {
		return ctx.String(http.StatusOK, "slug:"+ctx.Vars().Get("slug"))
	})

	for path, want := range map[string]string{
		"/items/123": "id:123",
		"/items/abc": "slug:abc",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if got := w.Body.String(); got != want {
			t.Errorf("%s body = %q, want %q", path, got, want)
		}
	}
}

func TestRouteMuxCustomVerbVariants(t *testing.T) {
	srv := NewServer()
	route := srv.Route("/")
	route.POST("/v1/{name}:archive", func(ctx Context) error {
		return ctx.String(http.StatusOK, "archive:"+ctx.Vars().Get("name"))
	})
	route.POST("/v1/{name}:delete", func(ctx Context) error {
		return ctx.String(http.StatusOK, "delete:"+ctx.Vars().Get("name"))
	})

	for path, want := range map[string]string{
		"/v1/books-1:archive": "archive:books-1",
		"/v1/books-1:delete":  "delete:books-1",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if got := w.Body.String(); got != want {
			t.Errorf("%s body = %q, want %q", path, got, want)
		}
	}
}

func TestRouteMuxPathValue(t *testing.T) {
	srv := NewServer()
	srv.Route("/").GET("/users/{user.name}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Request().PathValue("user.name"))
	})
	req := httptest.NewRequest(http.MethodGet, "/users/kratos", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if got := w.Body.String(); got != "kratos" {
		t.Fatalf("body = %q", got)
	}
}

func TestRouteMuxRootAndTrailingSlash(t *testing.T) {
	srv := NewServer()
	route := srv.Route("/")
	route.GET("/", func(ctx Context) error { return ctx.String(http.StatusOK, "root") })
	route.GET("/users/", func(ctx Context) error { return ctx.String(http.StatusOK, "users") })

	for path, want := range map[string]string{
		"/":       "root",
		"/users/": "users",
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%s: status = %d, body = %q", path, w.Code, w.Body.String())
		}
	}
}

func TestRouteMuxCustomVerbDoesNotMatchOtherSuffix(t *testing.T) {
	srv := NewServer()
	srv.Route("/").POST("/v1/{name}:archive", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("name"))
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/book:delete", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestRouteMuxGETMatchesHEAD(t *testing.T) {
	srv := NewServer()
	srv.Route("/").GET("/resource", func(ctx Context) error {
		return ctx.String(http.StatusOK, "resource")
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/resource", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestRouteMuxMoreSpecificPatternWins(t *testing.T) {
	srv := NewServer()
	route := srv.Route("/")
	route.GET("/users/{id}", func(ctx Context) error {
		return ctx.String(http.StatusOK, "variable")
	})
	route.GET("/users/current", func(ctx Context) error {
		return ctx.String(http.StatusOK, "literal")
	})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/users/current", nil))
	if got := w.Body.String(); got != "literal" {
		t.Fatalf("body = %q, want %q", got, "literal")
	}
}

func TestRouteMuxAllowsPathOverlapAcrossMethods(t *testing.T) {
	srv := NewServer(MethodNotAllowedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	route := srv.Route("/")
	route.GET("/{name}/foo", func(ctx Context) error { return ctx.String(http.StatusOK, "get") })
	route.POST("/bar/{name}", func(ctx Context) error { return ctx.String(http.StatusOK, "post") })

	for method, want := range map[string]string{
		http.MethodGet:  "get",
		http.MethodPost: "post",
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(method, "/bar/foo", nil))
		if w.Code != http.StatusOK || w.Body.String() != want {
			t.Errorf("%s: status = %d, body = %q", method, w.Code, w.Body.String())
		}
	}

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/bar/foo", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("PUT status = %d, want %d", w.Code, http.StatusTeapot)
	}
}

func TestRouteMuxPathPrefix(t *testing.T) {
	srv := NewServer()
	srv.HandlePrefix("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.URL.Path))
	}))

	for path := range map[string]struct{}{
		"/assets/":            {},
		"/assets/css/app.css": {},
	} {
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.String() != path {
			t.Errorf("%s: status = %d, body = %q", path, w.Code, w.Body.String())
		}
	}
}
