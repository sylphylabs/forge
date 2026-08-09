package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/host"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/transport"
)

var h = func(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(testData{Path: r.RequestURI})
}

type testKey struct{}

type testData struct {
	Path string `json:"path"`
}

// handleFuncWrapper is a wrapper for http.HandlerFunc to implement http.Handler
type handleFuncWrapper struct {
	fn http.HandlerFunc
}

func (x *handleFuncWrapper) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	x.fn.ServeHTTP(writer, request)
}

func newHandleFuncWrapper(fn http.HandlerFunc) http.Handler {
	return &handleFuncWrapper{fn: fn}
}

func TestServeHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	mux := NewServer(Listener(ln))
	mux.HandleFunc("/index", h)
	mux.Route("/errors").GET("/cause", func(Context) error {
		return forgeerrors.BadRequest("xxx", "zzz").
			WithMetadata(map[string]string{"foo": "bar"}).
			WithCause(errors.New("error cause"))
	})
	if err = mux.WalkRoute(func(r RouteInfo) error {
		t.Logf("WalkRoute: %+v", r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if e, err := mux.Endpoint(); err != nil || e == nil || strings.HasSuffix(e.Host, ":0") {
		t.Fatal(e, err)
	}
	srv := http.Server{Handler: mux}
	go func() {
		if err := srv.Serve(ln); err != nil {
			if forgeerrors.Is(err, http.ErrServerClosed) {
				return
			}
			panic(err)
		}
	}()
	time.Sleep(time.Second)
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Log(err)
	}
}

func TestServer(t *testing.T) {
	ctx := context.Background()
	srv := NewServer()
	srv.Handle("/index", newHandleFuncWrapper(h))
	srv.HandleFunc("/index/{id:[0-9]+}", h)
	srv.HandlePrefix("/test/prefix", newHandleFuncWrapper(h))
	srv.HandleHeader("content-type", "application/grpc-web+json", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(testData{Path: r.RequestURI})
	})
	srv.Route("/errors").GET("/cause", func(Context) error {
		return forgeerrors.BadRequest("xxx", "zzz").
			WithMetadata(map[string]string{"foo": "bar"}).
			WithCause(errors.New("error cause"))
	})

	if e, err := srv.Endpoint(); err != nil || e == nil || strings.HasSuffix(e.Host, ":0") {
		t.Fatal(e, err)
	}

	go func() {
		if err := srv.Start(ctx); err != nil {
			panic(err)
		}
	}()
	time.Sleep(time.Second)
	testHeader(t, srv)
	testClient(t, srv)
	testAccept(t, srv)
	time.Sleep(time.Second)
	if srv.Stop(ctx) != nil {
		t.Errorf("expected nil got %v", srv.Stop(ctx))
	}
}

func testAccept(t *testing.T, srv *Server) {
	tests := []struct {
		method      string
		path        string
		contentType string
	}{
		{http.MethodGet, "/errors/cause", "application/json"},
		{http.MethodGet, "/errors/cause", "application/proto"},
	}
	e, err := srv.Endpoint()
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	client, err := NewClient(context.Background(), WithEndpoint(e.Host))
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	for _, test := range tests {
		req, err := http.NewRequest(test.method, e.String()+test.path, nil)
		if err != nil {
			t.Errorf("expected nil got %v", err)
		}
		req.Header.Set("Content-Type", test.contentType)
		resp, err := client.Do(req)
		if forgeerrors.Code(err) != 400 {
			t.Errorf("expected 400 got %v", err)
		}
		if err == nil {
			resp.Body.Close()
		}
	}
}

func testHeader(t *testing.T, srv *Server) {
	e, err := srv.Endpoint()
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	client, err := NewClient(context.Background(), WithEndpoint(e.Host))
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, e.String()+"/index", nil)
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	req.Header.Set("content-type", "application/grpc-web+json")
	resp, err := client.Do(req)
	if err != nil {
		t.Errorf("expected nil got %v", err)
	}
	resp.Body.Close()
}

func testClient(t *testing.T, srv *Server) {
	tests := []struct {
		method string
		path   string
		code   int
	}{
		{http.MethodGet, "/index", http.StatusOK},
		{http.MethodPut, "/index", http.StatusOK},
		{http.MethodPost, "/index", http.StatusOK},
		{http.MethodPatch, "/index", http.StatusOK},
		{http.MethodDelete, "/index", http.StatusOK},

		{http.MethodGet, "/index/1", http.StatusOK},
		{http.MethodPut, "/index/1", http.StatusOK},
		{http.MethodPost, "/index/1", http.StatusOK},
		{http.MethodPatch, "/index/1", http.StatusOK},
		{http.MethodDelete, "/index/1", http.StatusOK},

		{http.MethodGet, "/index/notfound", http.StatusNotFound},
		{http.MethodGet, "/errors/cause", http.StatusBadRequest},
		{http.MethodGet, "/test/prefix/123111", http.StatusOK},
	}
	e, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(context.Background(), WithEndpoint(e.Host))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, test := range tests {
		var res testData
		reqURL := e.String() + test.path
		req, err := http.NewRequest(test.method, reqURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if forgeerrors.Code(err) != test.code {
			t.Fatalf("want %v, but got %v", test, err)
		}
		if err != nil {
			continue
		}
		if resp.StatusCode != 200 {
			_ = resp.Body.Close()
			t.Fatalf("http status got %d", resp.StatusCode)
		}
		content, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("read resp error %v", err)
		}
		err = json.Unmarshal(content, &res)
		if err != nil {
			t.Fatalf("unmarshal resp error %v", err)
		}
		if res.Path != test.path {
			t.Errorf("expected %s got %s", test.path, res.Path)
		}
	}
	for _, test := range tests {
		var res testData
		err := client.Invoke(context.Background(), test.method, test.path, nil, &res)
		if forgeerrors.Code(err) != test.code {
			t.Fatalf("want %v, but got %v", test, err)
		}
		if err != nil {
			continue
		}
		if res.Path != test.path {
			t.Errorf("expected %s got %s", test.path, res.Path)
		}
	}
}

func BenchmarkServer(b *testing.B) {
	fn := func(w http.ResponseWriter, r *http.Request) {
		data := &testData{Path: r.RequestURI}
		_ = json.NewEncoder(w).Encode(data)
		if r.Context().Value(testKey{}) != "test" {
			w.WriteHeader(500)
		}
	}
	ctx := context.Background()
	ctx = context.WithValue(ctx, testKey{}, "test")
	srv := NewServer()
	srv.HandleFunc("/index", fn)
	go func() {
		if err := srv.Start(ctx); err != nil {
			panic(err)
		}
	}()
	time.Sleep(time.Second)
	port, ok := host.Port(srv.lis)
	if !ok {
		b.Errorf("expected port got %v", srv.lis)
	}
	client, err := NewClient(context.Background(), WithEndpoint(fmt.Sprintf("127.0.0.1:%d", port)))
	if err != nil {
		b.Errorf("expected nil got %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var res testData
		err := client.Invoke(context.Background(), http.MethodPost, "/index", nil, &res)
		if err != nil {
			b.Errorf("expected nil got %v", err)
		}
	}
	_ = srv.Stop(ctx)
}

func TestNetwork(t *testing.T) {
	o := &Server{}
	v := "abc"
	Network(v)(o)
	if !reflect.DeepEqual(v, o.network) {
		t.Errorf("expected %v got %v", v, o.network)
	}
}

func TestAddress(t *testing.T) {
	o := &Server{}
	v := "abc"
	Address(v)(o)
	if !reflect.DeepEqual(v, o.address) {
		t.Errorf("expected %v got %v", v, o.address)
	}
}

func TestTimeout(t *testing.T) {
	o := &Server{}
	v := time.Duration(123)
	Timeout(v)(o)
	if !reflect.DeepEqual(v, o.timeout) {
		t.Errorf("expected %v got %v", v, o.timeout)
	}
}

func TestServerWithoutTimeoutPreservesRequestContext(t *testing.T) {
	type contextKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "value"))
	defer cancel()

	srv := NewServer(Timeout(0))
	srv.Route("").GET("/context", func(ctx Context) error {
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("unexpected deadline")
		}
		if got := ctx.Value(contextKey{}); got != "value" {
			t.Fatalf("context value = %v, want value", got)
		}
		cancel()
		select {
		case <-ctx.Done():
		default:
			t.Fatal("request cancellation did not propagate")
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/context", nil).WithContext(parent)
	srv.ServeHTTP(httptest.NewRecorder(), req)
}

func TestMatchedRoutePreservesRequestSemantics(t *testing.T) {
	type contextKey struct{}
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "value"))
	defer cancel()

	srv := NewServer(Timeout(0))
	srv.Route("").GET("/context/{name}", func(ctx Context) error {
		request := ctx.Request()
		if got := request.Pattern; got != "/context/{name}" {
			t.Fatalf("request pattern = %q, want %q", got, "/context/{name}")
		}
		if got := request.PathValue("name"); got != "forge" {
			t.Fatalf("path value = %q", got)
		}
		if got := request.PathValue("__forge0"); got != "" {
			t.Fatalf("internal path value = %q", got)
		}
		if got := ctx.Value(contextKey{}); got != "value" {
			t.Fatalf("context value = %v", got)
		}
		transportRequest, ok := RequestFromServerContext(ctx)
		if !ok || transportRequest != request {
			t.Fatal("transport request does not match handler request")
		}
		tr, ok := transport.FromServerContext(ctx)
		if !ok || tr.Operation() != "/context/{name}" {
			t.Fatalf("transport operation = %v", tr)
		}
		httpTransport, ok := tr.(Transporter)
		if !ok || httpTransport.PathTemplate() != "/context/{name}" {
			t.Fatalf("transport path template = %v", tr)
		}
		cancel()
		select {
		case <-ctx.Done():
		default:
			t.Fatal("request cancellation did not propagate")
		}
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "/context/forge", nil).WithContext(parent)
	srv.ServeHTTP(httptest.NewRecorder(), req)
}

func TestRequestDecoder(t *testing.T) {
	o := &Server{}
	v := func(*http.Request, any) error { return nil }
	RequestDecoder(v)(o)
	if o.decBody == nil {
		t.Errorf("expected nil got %v", o.decBody)
	}
}

func TestResponseEncoder(t *testing.T) {
	o := &Server{}
	v := func(http.ResponseWriter, *http.Request, any) error { return nil }
	ResponseEncoder(v)(o)
	if o.enc == nil {
		t.Errorf("expected nil got %v", o.enc)
	}
}

func TestErrorEncoder(t *testing.T) {
	o := &Server{}
	v := func(http.ResponseWriter, *http.Request, error) {}
	ErrorEncoder(v)(o)
	if o.ene == nil {
		t.Errorf("expected nil got %v", o.ene)
	}
}

func TestTLSConfig(t *testing.T) {
	o := &Server{}
	v := &tls.Config{}
	TLSConfig(v)(o)
	if !reflect.DeepEqual(v, o.tlsConf) {
		t.Errorf("expected %v got %v", v, o.tlsConf)
	}
}

func TestListener(t *testing.T) {
	lis, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	Listener(lis)(s)
	if !reflect.DeepEqual(s.lis, lis) {
		t.Errorf("expected %v got %v", lis, s.lis)
	}
	if e, err := s.Endpoint(); err != nil || e == nil {
		t.Errorf("expected not empty")
	}
}

func TestNotFoundHandler(t *testing.T) {
	srv := NewServer(NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
}

func TestMethodNotAllowedHandler(t *testing.T) {
	srv := NewServer(MethodNotAllowedHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	srv.Route("/").GET("/resource", func(Context) error { return nil })
	recorder := httptest.NewRecorder()
	srv.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/resource", nil))
	if recorder.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusTeapot)
	}
}

func TestStop(t *testing.T) {
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	tests := []struct {
		name          string
		sleep         time.Duration
		ctx           context.Context
		cancel        context.CancelFunc
		wantForceStop bool
	}{
		{
			name:          "normal",
			sleep:         0,
			ctx:           context.Background(),
			cancel:        func() {},
			wantForceStop: false,
		},
		{
			name:          "timeout",
			sleep:         2 * time.Second,
			ctx:           timeoutCtx,
			cancel:        cancel,
			wantForceStop: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := log.Default()
			defer log.SetDefault(old)

			// Create a logger to capture logs
			var logs safeBytesBuffer
			log.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))

			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t := time.NewTimer(tt.sleep)
				defer t.Stop()
				select {
				case <-t.C:
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer testServer.Close()

			go func() {
				resp, err := http.Get(testServer.URL)
				if err != nil {
					return
				}
				_ = resp.Body.Close()
			}()

			time.Sleep(100 * time.Millisecond)

			s := &Server{
				Server: testServer.Config,
			}

			tt.cancel()
			err := s.Stop(tt.ctx)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
				return
			}

			// Check if the stop was forced or graceful
			if tt.wantForceStop {
				if !strings.Contains(logs.String(), "force stop") {
					t.Errorf("Expected force stop\n%s", logs.String())
				}
			} else {
				if strings.Contains(logs.String(), "force stop") {
					t.Errorf("Expected graceful stop\n%s", logs.String())
				}
			}
		})
	}
}

type safeBytesBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBytesBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBytesBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestHealthzLifecycle(t *testing.T) {
	srv := NewServer(Address("127.0.0.1:0"))
	if srv.Healthz() {
		t.Fatal("Healthz() before Start = true, want false")
	}
	go func() {
		_ = srv.Start(context.Background())
	}()
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Healthz() {
		if time.Now().After(deadline) {
			t.Fatal("Healthz() never became true after Start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := srv.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if srv.Healthz() {
		t.Fatal("Healthz() after Stop = true, want false")
	}
}

func TestGracefulStopDrainsInFlightRequests(t *testing.T) {
	inHandler := make(chan struct{})
	release := make(chan struct{})
	srv := NewServer(Address("127.0.0.1:0"), Timeout(0))
	srv.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(inHandler)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	endpoint, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Start(context.Background()) }()

	got := make(chan int, 1)
	go func() {
		resp, err := http.Get(endpoint.String() + "/slow")
		if err != nil {
			got <- -1
			return
		}
		defer resp.Body.Close()
		got <- resp.StatusCode
	}()
	<-inHandler
	if !srv.Healthz() {
		t.Fatal("Healthz() while serving = false, want true")
	}

	stopped := make(chan error, 1)
	go func() { stopped <- srv.GracefulStop(context.Background()) }()
	// Draining begins: readiness must already be false while the in-flight
	// request is still being served.
	deadline := time.Now().Add(5 * time.Second)
	for srv.Healthz() {
		if time.Now().After(deadline) {
			t.Fatal("Healthz() stayed true after GracefulStop began")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	if err := <-stopped; err != nil {
		t.Fatalf("GracefulStop() = %v, want nil", err)
	}
	if code := <-got; code != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want %d", code, http.StatusOK)
	}
}

func TestGracefulStopAbandonsDrainOnContextEnd(t *testing.T) {
	inHandler := make(chan struct{})
	release := make(chan struct{})
	srv := NewServer(Address("127.0.0.1:0"), Timeout(0))
	srv.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(inHandler)
		<-release
		w.WriteHeader(http.StatusOK)
	})
	endpoint, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Start(context.Background()) }()

	got := make(chan int, 1)
	go func() {
		resp, err := http.Get(endpoint.String() + "/slow")
		if err != nil {
			got <- -1
			return
		}
		defer resp.Body.Close()
		got <- resp.StatusCode
	}()
	<-inHandler

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := srv.GracefulStop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("GracefulStop() = %v, want %v", err, context.DeadlineExceeded)
	}
	// The drain keeps running in the background: releasing the handler lets
	// the in-flight request complete instead of being force-closed.
	close(release)
	if code := <-got; code != http.StatusOK {
		t.Fatalf("in-flight request status = %d, want %d", code, http.StatusOK)
	}
	_ = srv.Stop(context.Background())
}
