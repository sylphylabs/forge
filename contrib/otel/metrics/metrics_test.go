package metrics

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/sylphylabs/forge/registry"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

var durationBounds = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25,
	0.5, 0.75, 1, 2.5, 5, 7.5, 10,
}

func TestHTTPConstructorsRejectInvalidConfiguration(t *testing.T) {
	if _, err := NewHTTPServerFilter(nil); err == nil {
		t.Fatal("NewHTTPServerFilter(nil) succeeded")
	}
	if _, err := NewHTTPClientWrapper(nil); err == nil {
		t.Fatal("NewHTTPClientWrapper(nil) succeeded")
	}

	var typedNilProvider *sdkmetric.MeterProvider
	if _, err := NewHTTPServerFilter(typedNilProvider); err == nil {
		t.Fatal("NewHTTPServerFilter(typed nil) succeeded")
	}
	if _, err := NewHTTPClientWrapper(typedNilProvider); err == nil {
		t.Fatal("NewHTTPClientWrapper(typed nil) succeeded")
	}

	provider := sdkmetric.NewMeterProvider()
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	if _, err := NewHTTPServerFilter(provider, nil); err == nil {
		t.Fatal("NewHTTPServerFilter(nil option) succeeded")
	}
	if _, err := NewHTTPClientWrapper(provider, nil); err == nil {
		t.Fatal("NewHTTPClientWrapper(nil option) succeeded")
	}
	if _, err := NewHTTPServerFilter(provider, WithHTTPServerKnownMethods("NOT VALID")); err == nil {
		t.Fatal("NewHTTPServerFilter(invalid method) succeeded")
	}
	if _, err := NewHTTPClientWrapper(provider, WithHTTPClientKnownMethods("NOT VALID")); err == nil {
		t.Fatal("NewHTTPClientWrapper(invalid method) succeeded")
	}

	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	if _, err := wrapper(nil); err == nil {
		t.Fatal("client wrapper accepted a nil base RoundTripper")
	}
	var typedNilTransport roundTripperFunc
	if _, err := wrapper(typedNilTransport); err == nil {
		t.Fatal("client wrapper accepted a typed nil base RoundTripper")
	}

	wantErr := errors.New("instrument failure")
	failing := failingMeterProvider{err: wantErr}
	if _, err := NewHTTPServerFilter(failing); !errors.Is(err, wantErr) {
		t.Fatalf("NewHTTPServerFilter() error = %v, want wrapped %v", err, wantErr)
	}
	if _, err := NewHTTPClientWrapper(failing); !errors.Is(err, wantErr) {
		t.Fatalf("NewHTTPClientWrapper() error = %v, want wrapped %v", err, wantErr)
	}
	for name, provider := range map[string]metric.MeterProvider{
		"nil meter":      nilMeterProvider{},
		"nil instrument": nilInstrumentProvider{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewHTTPServerFilter(provider); err == nil {
				t.Fatal("NewHTTPServerFilter() accepted invalid provider result")
			}
			if _, err := NewHTTPClientWrapper(provider); err == nil {
				t.Fatal("NewHTTPClientWrapper() accepted invalid provider result")
			}
		})
	}
}

func TestHTTPServerDurationContract(t *testing.T) {
	tests := []struct {
		name       string
		request    func() *http.Request
		handler    http.HandlerFunc
		wantAttrs  []attribute.KeyValue
		wantStatus int
	}{
		{
			name:    "normal return has implicit 200 and canonical route",
			request: func() *http.Request { return httptest.NewRequest(http.MethodGet, "http://example.test/users/42", nil) },
			handler: func(_ http.ResponseWriter, request *http.Request) {
				request.Pattern = "GET /users/{id}"
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("http.route", "/users/{id}"),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 200,
		},
		{
			name:    "4xx is not a server error",
			request: func() *http.Request { return httptest.NewRequest(http.MethodGet, "http://example.test/missing", nil) },
			handler: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNotFound) },
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 404),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 404,
		},
		{
			name:    "5xx is classified by status",
			request: func() *http.Request { return httptest.NewRequest(http.MethodPost, "http://example.test/jobs", nil) },
			handler: func(writer http.ResponseWriter, request *http.Request) {
				request.Pattern = "POST /jobs"
				writer.WriteHeader(http.StatusServiceUnavailable)
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "POST"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 503),
				attribute.String("http.route", "/jobs"),
				attribute.String("network.protocol.version", "1.1"),
				attribute.String("error.type", "503"),
			},
			wantStatus: 503,
		},
		{
			name:    "temporary informational response does not commit",
			request: func() *http.Request { return httptest.NewRequest(http.MethodHead, "http://example.test/info", nil) },
			handler: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusEarlyHints) },
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "HEAD"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 200,
		},
		{
			name:    "101 is a final response",
			request: func() *http.Request { return httptest.NewRequest(http.MethodGet, "http://example.test/socket", nil) },
			handler: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusSwitchingProtocols) },
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 101),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 101,
		},
		{
			name: "TLS request uses https scheme",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodPut, "https://example.test/items/1", nil)
				request.TLS = new(tls.ConnectionState)
				return request
			},
			handler: func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "ok") },
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "PUT"),
				attribute.String("url.scheme", "https"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 200,
		},
		{
			name: "internal mux pattern is never exposed",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodGet, "http://example.test/private/42", nil)
			},
			handler: func(_ http.ResponseWriter, request *http.Request) {
				request.Pattern = "GET /private/{__forge0}"
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			},
			wantStatus: 200,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			filter, err := NewHTTPServerFilter(provider)
			if err != nil {
				t.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			filter(test.handler).ServeHTTP(httptest.NewRecorder(), test.request())
			observation := collectSingleHistogram(t, reader, "http.server.request.duration", test.wantAttrs)
			assertDurationInstrument(t, observation)
			status, ok := observation.point.Attributes.Value("http.response.status_code")
			if !ok || int(status.AsInt64()) != test.wantStatus {
				t.Fatalf("status attribute = %v, %v, want %d", status, ok, test.wantStatus)
			}
		})
	}
}

func TestHTTPServerForgeRouteIntegration(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantRoute  string
	}{
		{
			name:       "complex Google route",
			method:     http.MethodGet,
			path:       "/v1/publishers/acme/books/42",
			wantStatus: http.StatusOK,
			wantRoute:  "/v1/{message.name=publishers/*/books/*}",
		},
		{
			name:       "candidate mismatch",
			method:     http.MethodGet,
			path:       "/items/not-a-number",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "method not allowed",
			method:     http.MethodPost,
			path:       "/items/123",
			wantStatus: http.StatusMethodNotAllowed,
		},
		{
			name:       "not found",
			method:     http.MethodGet,
			path:       "/missing",
			wantStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			filter, err := NewHTTPServerFilter(provider)
			if err != nil {
				t.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			server := forgehttp.NewServer(forgehttp.Filter(filter))
			router := server.Route("/")
			router.GET("/v1/{message.name=publishers/*/books/*}", func(ctx forgehttp.Context) error {
				return ctx.String(http.StatusOK, "ok")
			})
			router.GET("/items/{id:[0-9]+}", func(ctx forgehttp.Context) error {
				return ctx.String(http.StatusOK, "ok")
			})

			request := httptest.NewRequest(test.method, test.path, nil)
			request.Pattern = "stale pattern"
			writer := httptest.NewRecorder()
			server.ServeHTTP(writer, request)
			if writer.Code != test.wantStatus {
				t.Fatalf("response status = %d, want %d", writer.Code, test.wantStatus)
			}
			wantAttrs := []attribute.KeyValue{
				attribute.String("http.request.method", test.method),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", test.wantStatus),
				attribute.String("network.protocol.version", "1.1"),
			}
			if test.wantRoute != "" {
				wantAttrs = append(wantAttrs, attribute.String("http.route", test.wantRoute))
			}
			collectSingleHistogram(t, reader, "http.server.request.duration", wantAttrs)
		})
	}
}

func TestProtocolVersion(t *testing.T) {
	tests := []struct {
		name         string
		major, minor int
		want         string
	}{
		{name: "HTTP 1.0", major: 1, minor: 0, want: "1.0"},
		{name: "HTTP 1.1", major: 1, minor: 1, want: "1.1"},
		{name: "HTTP 2", major: 2, minor: 0, want: "2"},
		{name: "HTTP 3", major: 3, minor: 0, want: "3"},
		{name: "unknown fields", major: 0, minor: 0, want: ""},
		{name: "invalid minor", major: 1, minor: -1, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := protocolVersion(test.major, test.minor); got != test.want {
				t.Fatalf("protocolVersion(%d, %d) = %q, want %q", test.major, test.minor, got, test.want)
			}
		})
	}
}

func TestHTTPKnownMethodsAreCaseSensitiveFullReplacements(t *testing.T) {
	serverTests := []struct {
		name       string
		methods    []string
		request    string
		wantMethod string
	}{
		{name: "custom known", methods: []string{"BREW"}, request: "BREW", wantMethod: "BREW"},
		{name: "default removed", methods: []string{"BREW"}, request: http.MethodGet, wantMethod: "_OTHER"},
		{name: "case sensitive", methods: []string{http.MethodGet}, request: "get", wantMethod: "_OTHER"},
		{name: "empty replacement", methods: nil, request: http.MethodGet, wantMethod: "_OTHER"},
	}
	for _, test := range serverTests {
		t.Run("server "+test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			filter, err := NewHTTPServerFilter(provider, WithHTTPServerKnownMethods(test.methods...))
			if err != nil {
				t.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			request := httptest.NewRequest(test.request, "http://example.test/", nil)
			filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), request)
			collectSingleHistogram(t, reader, "http.server.request.duration", []attribute.KeyValue{
				attribute.String("http.request.method", test.wantMethod),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			})
		})
	}

	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider, WithHTTPClientKnownMethods("BREW"))
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 204, Proto: "HTTP/2.0", ProtoMajor: 2, ProtoMinor: 0, Body: http.NoBody}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.test/path", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	_ = response.Body.Close()
	collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "_OTHER"),
		attribute.String("server.address", "example.test"),
		attribute.Int("server.port", 443),
		attribute.Int("http.response.status_code", 204),
		attribute.String("network.protocol.version", "2"),
	})
}

func TestHTTPServerWriteError(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	filter, err := NewHTTPServerFilter(provider)
	if err != nil {
		t.Fatalf("NewHTTPServerFilter() failed: %v", err)
	}
	wantErr := writeFailure{}
	writer := &errorResponseWriter{header: make(http.Header), err: wantErr}
	filter(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("response"))
	})).ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))

	collectSingleHistogram(t, reader, "http.server.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("url.scheme", "http"),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
		semconv.ErrorType(wantErr),
	})
}

func TestHTTPServerPanicIsRecordedAndRethrown(t *testing.T) {
	tests := []struct {
		name      string
		handler   http.HandlerFunc
		wantAttrs []attribute.KeyValue
	}{
		{
			name:    "before response",
			handler: func(http.ResponseWriter, *http.Request) { panic("sentinel") },
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.String("network.protocol.version", "1.1"),
				attribute.String("error.type", "_OTHER"),
			},
		},
		{
			name: "after response",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusBadGateway)
				panic("sentinel")
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 502),
				attribute.String("network.protocol.version", "1.1"),
				attribute.String("error.type", "_OTHER"),
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			filter, err := NewHTTPServerFilter(provider)
			if err != nil {
				t.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				filter(test.handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
			}()
			if recovered != "sentinel" {
				t.Fatalf("recovered panic = %#v, want sentinel", recovered)
			}
			collectSingleHistogram(t, reader, "http.server.request.duration", test.wantAttrs)
		})
	}
}

func TestHTTPServerPreservesResponseWriterCapabilities(t *testing.T) {
	t.Run("full writer", func(t *testing.T) {
		reader, provider := newTestMeterProvider(t)
		filter, err := NewHTTPServerFilter(provider)
		if err != nil {
			t.Fatalf("NewHTTPServerFilter() failed: %v", err)
		}
		base := newFullResponseWriter()
		filter(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			if _, ok := writer.(http.Flusher); !ok {
				t.Error("wrapped writer lost http.Flusher")
			}
			if _, ok := writer.(http.Hijacker); !ok {
				t.Error("wrapped writer lost http.Hijacker")
			}
			if _, ok := writer.(http.Pusher); !ok {
				t.Error("wrapped writer lost http.Pusher")
			}
			if _, ok := writer.(io.ReaderFrom); !ok {
				t.Error("wrapped writer lost io.ReaderFrom")
			}
			if _, ok := writer.(io.StringWriter); !ok {
				t.Error("wrapped writer lost io.StringWriter")
			}
			if unwrapped := httpsnoop.Unwrap(writer); unwrapped != base {
				t.Errorf("httpsnoop.Unwrap(writer) = %T %p, want base %p", unwrapped, unwrapped, base)
			}
			controller := http.NewResponseController(writer)
			if err := controller.SetReadDeadline(time.Now()); err != nil {
				t.Errorf("SetReadDeadline() failed: %v", err)
			}
			if err := controller.SetWriteDeadline(time.Now()); err != nil {
				t.Errorf("SetWriteDeadline() failed: %v", err)
			}
			if err := controller.EnableFullDuplex(); err != nil {
				t.Errorf("EnableFullDuplex() failed: %v", err)
			}
			if _, err := writer.(io.StringWriter).WriteString("a"); err != nil {
				t.Errorf("WriteString() failed: %v", err)
			}
			if _, err := writer.(io.ReaderFrom).ReadFrom(&plainReader{data: []byte("b")}); err != nil {
				t.Errorf("ReadFrom() failed: %v", err)
			}
			if err := controller.Flush(); err != nil {
				t.Errorf("Flush() failed: %v", err)
			}
		})).ServeHTTP(base, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		collectSingleHistogram(t, reader, "http.server.request.duration", []attribute.KeyValue{
			attribute.String("http.request.method", "GET"),
			attribute.String("url.scheme", "http"),
			attribute.Int("http.response.status_code", 200),
			attribute.String("network.protocol.version", "1.1"),
		})
	})

	t.Run("bare writer", func(t *testing.T) {
		reader, provider := newTestMeterProvider(t)
		filter, err := NewHTTPServerFilter(provider)
		if err != nil {
			t.Fatalf("NewHTTPServerFilter() failed: %v", err)
		}
		base := &bareResponseWriter{header: make(http.Header)}
		filter(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			if _, ok := writer.(http.Flusher); ok {
				t.Error("wrapped writer invented http.Flusher")
			}
			if _, ok := writer.(http.Hijacker); ok {
				t.Error("wrapped writer invented http.Hijacker")
			}
			if _, ok := writer.(http.Pusher); ok {
				t.Error("wrapped writer invented http.Pusher")
			}
			if _, ok := writer.(io.ReaderFrom); ok {
				t.Error("wrapped writer invented io.ReaderFrom")
			}
			if _, ok := writer.(io.StringWriter); ok {
				t.Error("wrapped writer invented io.StringWriter")
			}
			if unwrapped := httpsnoop.Unwrap(writer); unwrapped != base {
				t.Errorf("httpsnoop.Unwrap(writer) = %T %p, want base %p", unwrapped, unwrapped, base)
			}
		})).ServeHTTP(base, httptest.NewRequest(http.MethodGet, "http://example.test/", nil))
		collectSingleHistogram(t, reader, "http.server.request.duration", []attribute.KeyValue{
			attribute.String("http.request.method", "GET"),
			attribute.String("url.scheme", "http"),
			attribute.Int("http.response.status_code", 200),
			attribute.String("network.protocol.version", "1.1"),
		})
	})
}

func TestHTTPServerDurationCoversSSELifecycle(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	filter, err := NewHTTPServerFilter(provider)
	if err != nil {
		t.Fatalf("NewHTTPServerFilter() failed: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	handler := filter(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.(http.Flusher).Flush()
		close(started)
		<-release
		// A stream-level failure after the HTTP response starts cannot become a
		// different HTTP outcome.
	}))
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://example.test/events", nil))
	}()
	<-started
	if got := histogramCount(t, reader, "http.server.request.duration"); got != 0 {
		t.Fatalf("duration count while SSE handler is active = %d, want 0", got)
	}
	close(release)
	<-done
	collectSingleHistogram(t, reader, "http.server.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("url.scheme", "http"),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
}

func TestHTTPServerHijackStatusRules(t *testing.T) {
	tests := []struct {
		name      string
		request   func() *http.Request
		hijackErr error
		wantAttrs []attribute.KeyValue
	}{
		{
			name: "valid upgrade infers 101",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "http://example.test/socket", nil)
				request.Header.Set("Connection", "keep-alive, Upgrade")
				request.Header.Set("Upgrade", "websocket/13, h2c")
				return request
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 101),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name: "valid upgrade tolerates empty list elements",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "http://example.test/socket", nil)
				request.Header.Set("Connection", "upgrade")
				request.Header.Set("Upgrade", ", websocket/13,,")
				return request
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 101),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name: "CONNECT hijack has no inferred status",
			request: func() *http.Request {
				return httptest.NewRequest(http.MethodConnect, "http://example.test/tunnel", nil)
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "CONNECT"),
				attribute.String("url.scheme", "http"),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name: "invalid Upgrade protocol has no inferred status",
			request: func() *http.Request {
				request := httptest.NewRequest(http.MethodGet, "http://example.test/socket", nil)
				request.Header.Set("Connection", "upgrade")
				request.Header.Set("Upgrade", "not a protocol")
				return request
			},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name:      "failed hijack is an error and normal return is 200",
			request:   func() *http.Request { return httptest.NewRequest(http.MethodGet, "http://example.test/socket", nil) },
			hijackErr: hijackFailure{},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("url.scheme", "http"),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
				semconv.ErrorType(hijackFailure{}),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			filter, err := NewHTTPServerFilter(provider)
			if err != nil {
				t.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			writer := &hijackResponseWriter{header: make(http.Header), err: test.hijackErr}
			filter(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				conn, _, err := writer.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
			})).ServeHTTP(writer, test.request())
			writer.close()
			collectSingleHistogram(t, reader, "http.server.request.duration", test.wantAttrs)
		})
	}
}

func TestHTTPClientDurationContract(t *testing.T) {
	transportErr := transportFailure{}
	tests := []struct {
		name      string
		method    string
		url       string
		response  *http.Response
		err       error
		wantAttrs []attribute.KeyValue
	}{
		{
			name:     "HTTP default port",
			method:   http.MethodGet,
			url:      "http://example.test/path?secret=value",
			response: &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 80),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name:     "HTTPS IPv6 default port",
			method:   http.MethodPatch,
			url:      "https://[2001:db8::1]/items/42",
			response: &http.Response{StatusCode: 204, Proto: "HTTP/2.0", ProtoMajor: 2, ProtoMinor: 0, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "PATCH"),
				attribute.String("server.address", "2001:db8::1"),
				attribute.Int("server.port", 443),
				attribute.Int("http.response.status_code", 204),
				attribute.String("network.protocol.version", "2"),
			},
		},
		{
			name:     "explicit IPv6 port",
			method:   "QUERY",
			url:      "https://[2001:db8::2]:8443/search",
			response: &http.Response{StatusCode: 200, Proto: "HTTP/3.0", ProtoMajor: 3, ProtoMinor: 0, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "QUERY"),
				attribute.String("server.address", "2001:db8::2"),
				attribute.Int("server.port", 8443),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "3"),
			},
		},
		{
			name:     "invalid explicit port does not use scheme default",
			method:   http.MethodGet,
			url:      "http://example.test:70000/path",
			response: &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 0),
				attribute.Int("http.response.status_code", 200),
				attribute.String("network.protocol.version", "1.1"),
			},
		},
		{
			name:     "4xx is a client error",
			method:   http.MethodDelete,
			url:      "http://example.test/item/1",
			response: &http.Response{StatusCode: 404, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "DELETE"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 80),
				attribute.Int("http.response.status_code", 404),
				attribute.String("network.protocol.version", "1.1"),
				attribute.String("error.type", "404"),
			},
		},
		{
			name:     "5xx is a client error",
			method:   http.MethodPost,
			url:      "https://example.test/jobs",
			response: &http.Response{StatusCode: 503, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "POST"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 443),
				attribute.Int("http.response.status_code", 503),
				attribute.String("network.protocol.version", "1.1"),
				attribute.String("error.type", "503"),
			},
		},
		{
			name:     "unknown protocol string is not guessed",
			method:   http.MethodGet,
			url:      "http://example.test/unknown-protocol",
			response: &http.Response{StatusCode: 200, Proto: "ALIEN/9.9", Body: http.NoBody},
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 80),
				attribute.Int("http.response.status_code", 200),
			},
		},
		{
			name:   "transport error uses semantic error type",
			method: "BREW",
			url:    "http://example.test/coffee",
			err:    transportErr,
			wantAttrs: []attribute.KeyValue{
				attribute.String("http.request.method", "_OTHER"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 80),
				semconv.ErrorType(transportErr),
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			wrapper, err := NewHTTPClientWrapper(provider)
			if err != nil {
				t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
			}
			transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return test.response, test.err
			}))
			if err != nil {
				t.Fatalf("wrapper(base) failed: %v", err)
			}
			request, err := http.NewRequest(test.method, test.url, nil)
			if err != nil {
				t.Fatalf("http.NewRequest() failed: %v", err)
			}
			response, gotErr := transport.RoundTrip(request)
			if response != test.response || !errors.Is(gotErr, test.err) {
				t.Fatalf("RoundTrip() = (%p, %v), want (%p, %v)", response, gotErr, test.response, test.err)
			}
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			observation := collectSingleHistogram(t, reader, "http.client.request.duration", test.wantAttrs)
			assertDurationInstrument(t, observation)
		})
	}
}

func TestHTTPClientAddressDoesNotUseRequestHost(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       http.NoBody,
		}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "/relative", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	request.Host = "user-controlled.example:9443"
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	_ = response.Body.Close()
	collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", ""),
		attribute.Int("server.port", 0),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
}

func TestHTTPClientAttributesAreSnapshottedBeforeBaseMutation(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		request.Method = http.MethodPost
		request.URL.Scheme = "https"
		request.URL.Host = "mutated.example:9443"
		return &http.Response{
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       http.NoBody,
		}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://logical.example:8080/path", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	_ = response.Body.Close()
	collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "logical.example"),
		attribute.Int("server.port", 8080),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
}

func TestHTTPClientDiscoveryDirectDoUsesConfiguredAuthority(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}

	const physicalNode = "192.0.2.20:18080"
	var receivedAuthority string
	client, err := forgehttp.NewClient(
		t.Context(),
		forgehttp.WithEndpoint("discovery:///catalog.service"),
		forgehttp.WithDiscovery(staticDiscovery{instances: []*registry.ServiceInstance{{
			ID:        "node-1",
			Name:      "catalog.service",
			Version:   "v1",
			Endpoints: []string{"http://" + physicalNode},
		}}}),
		forgehttp.WithBlock(),
		forgehttp.WithTransport(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			receivedAuthority = request.URL.Host
			return &http.Response{
				StatusCode: http.StatusOK,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     make(http.Header),
				Body:       http.NoBody,
			}, nil
		})),
		forgehttp.WithRoundTripperWrapper(wrapper),
		forgehttp.WithErrorDecoder(func(context.Context, *http.Response) error { return nil }),
	)
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := client.Close(); closeErr != nil {
			t.Errorf("Client.Close() failed: %v", closeErr)
		}
	})
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "discovery:///v1/items", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() failed: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("Do() failed: %v", err)
	}
	_ = response.Body.Close()
	if receivedAuthority != physicalNode {
		t.Fatalf("base transport authority = %q, want %q", receivedAuthority, physicalNode)
	}
	observation := collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "catalog.service"),
		attribute.Int("server.port", 80),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
	assertDurationInstrument(t, observation)
}

func TestHTTPClientDiscoveryUsesLogicalAuthorityAcrossRedirect(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}

	const physicalNode = "192.0.2.10:18080"
	var receivedAuthorities []string
	base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		receivedAuthorities = append(receivedAuthorities, request.URL.Host)
		if len(receivedAuthorities) == 1 {
			return &http.Response{
				StatusCode: http.StatusFound,
				Proto:      "HTTP/1.1",
				ProtoMajor: 1,
				ProtoMinor: 1,
				Header:     http.Header{"Location": {"http://redirect.example:8081/final"}},
				Body:       http.NoBody,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})
	discovery := staticDiscovery{instances: []*registry.ServiceInstance{{
		ID:        "node-1",
		Name:      "catalog.service",
		Version:   "v1",
		Endpoints: []string{"http://" + physicalNode},
	}}}
	client, err := forgehttp.NewClient(
		t.Context(),
		forgehttp.WithEndpoint("discovery:///catalog.service"),
		forgehttp.WithDiscovery(discovery),
		forgehttp.WithBlock(),
		forgehttp.WithTransport(base),
		forgehttp.WithRoundTripperWrapper(wrapper),
		forgehttp.WithResponseDecoder(func(context.Context, *http.Response, any) error { return nil }),
		forgehttp.WithErrorDecoder(func(context.Context, *http.Response) error { return nil }),
	)
	if err != nil {
		t.Fatalf("NewClient() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Client.Close() failed: %v", err)
		}
	})
	if err := client.Invoke(t.Context(), http.MethodGet, "/v1/items", nil, nil); err != nil {
		t.Fatalf("Invoke() failed: %v", err)
	}

	wantAuthorities := []string{physicalNode, "redirect.example:8081"}
	if !slices.Equal(receivedAuthorities, wantAuthorities) {
		t.Fatalf("base transport authorities = %v, want %v", receivedAuthorities, wantAuthorities)
	}
	wantAttributeSets := [][]attribute.KeyValue{
		{
			attribute.String("http.request.method", "GET"),
			attribute.String("server.address", "catalog.service"),
			attribute.Int("server.port", 80),
			attribute.Int("http.response.status_code", 302),
			attribute.String("network.protocol.version", "1.1"),
		},
		{
			attribute.String("http.request.method", "GET"),
			attribute.String("server.address", "redirect.example"),
			attribute.Int("server.port", 8081),
			attribute.Int("http.response.status_code", 200),
			attribute.String("network.protocol.version", "1.1"),
		},
	}
	observations := collectHistograms(t, reader, "http.client.request.duration")
	if len(observations) != len(wantAttributeSets) {
		t.Fatalf("http.client.request.duration data point count = %d, want %d", len(observations), len(wantAttributeSets))
	}
	for _, wantAttrs := range wantAttributeSets {
		want := attribute.NewSet(wantAttrs...)
		var found bool
		for _, observation := range observations {
			if observation.point.Attributes.Equals(&want) {
				assertDurationInstrument(t, observation)
				found = true
				break
			}
		}
		if !found {
			t.Errorf("http.client.request.duration missing attributes %v", want.ToSlice())
		}
	}
}

func TestHTTPClientPanicIsRecordedAndRethrown(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		panic("sentinel")
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://example.test/path", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		response, roundTripErr := transport.RoundTrip(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		_ = roundTripErr
	}()
	if recovered != "sentinel" {
		t.Fatalf("recovered panic = %#v, want sentinel", recovered)
	}
	collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "example.test"),
		attribute.Int("server.port", 443),
		attribute.String("error.type", "_OTHER"),
	})
}

func TestHTTPClientDurationExcludesResponseBody(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	body := new(countingBody)
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: body}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/stream", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	defer response.Body.Close()
	if body.reads != 0 {
		t.Fatalf("body reads before RoundTrip returned = %d, want 0", body.reads)
	}
	wantAttrs := []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "example.test"),
		attribute.Int("server.port", 80),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	}
	before := collectSingleHistogram(t, reader, "http.client.request.duration", wantAttrs).point
	if _, err := response.Body.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("Body.Read() error = %v, want EOF", err)
	}
	after := collectSingleHistogram(t, reader, "http.client.request.duration", wantAttrs).point
	if body.reads != 1 {
		t.Fatalf("body reads after explicit read = %d, want 1", body.reads)
	}
	if after.Count != 1 || after.Sum != before.Sum {
		t.Fatalf("histogram changed after body read: before count/sum = %d/%v, after = %d/%v", before.Count, before.Sum, after.Count, after.Sum)
	}
}

func TestHTTPClientDurationIncludesRequestUpload(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	body := &gatedRequestBody{started: started, release: release}
	transport, err := wrapper(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if _, copyErr := io.Copy(io.Discard, request.Body); copyErr != nil {
			return nil, copyErr
		}
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       http.NoBody,
		}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, "http://example.test/upload", body)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		response, roundTripErr := transport.RoundTrip(request)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		done <- roundTripErr
	}()
	<-started
	if got := histogramCount(t, reader, "http.client.request.duration"); got != 0 {
		t.Fatalf("duration count while request upload is active = %d, want 0", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "POST"),
		attribute.String("server.address", "example.test"),
		attribute.Int("server.port", 80),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
}

func TestHTTPClientCancellationAndDeadlineErrors(t *testing.T) {
	tests := []struct {
		name    string
		context func() context.Context
	}{
		{
			name: "canceled",
			context: func() context.Context {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx
			},
		},
		{
			name: "deadline exceeded",
			context: func() context.Context {
				ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
				t.Cleanup(cancel)
				return ctx
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, provider := newTestMeterProvider(t)
			wrapper, err := NewHTTPClientWrapper(provider)
			if err != nil {
				t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
			}
			transport, err := wrapper(roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return nil, request.Context().Err()
			}))
			if err != nil {
				t.Fatalf("wrapper(base) failed: %v", err)
			}
			request, err := http.NewRequestWithContext(test.context(), http.MethodGet, "https://example.test/", nil)
			if err != nil {
				t.Fatalf("http.NewRequestWithContext() failed: %v", err)
			}
			response, gotErr := transport.RoundTrip(request)
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			if gotErr == nil {
				t.Fatal("RoundTrip() succeeded")
			}
			collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
				attribute.String("http.request.method", "GET"),
				attribute.String("server.address", "example.test"),
				attribute.Int("server.port", 443),
				semconv.ErrorType(gotErr),
			})
		})
	}
}

func TestHTTPMeterProviderIsolation(t *testing.T) {
	firstReader, firstProvider := newTestMeterProvider(t)
	secondReader, secondProvider := newTestMeterProvider(t)
	firstFilter, err := NewHTTPServerFilter(firstProvider)
	if err != nil {
		t.Fatalf("NewHTTPServerFilter(first) failed: %v", err)
	}
	if _, err := NewHTTPServerFilter(secondProvider); err != nil {
		t.Fatalf("NewHTTPServerFilter(second) failed: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	firstFilter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), request)
	if got := histogramCount(t, firstReader, "http.server.request.duration"); got != 1 {
		t.Fatalf("first provider count = %d, want 1", got)
	}
	if got := histogramCount(t, secondReader, "http.server.request.duration"); got != 0 {
		t.Fatalf("second provider recorded first provider request: count = %d", got)
	}
}

func TestHTTPDurationOnlyMetricSet(t *testing.T) {
	reader, provider := newTestMeterProvider(t)
	filter, err := NewHTTPServerFilter(provider)
	if err != nil {
		t.Fatalf("NewHTTPServerFilter() failed: %v", err)
	}
	filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "http://example.test/", nil),
	)
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 200,
			Proto:      "HTTP/1.1",
			ProtoMajor: 1,
			ProtoMinor: 1,
			Body:       http.NoBody,
		}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	request, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	_ = response.Body.Close()

	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resourceMetrics); err != nil {
		t.Fatalf("ManualReader.Collect() failed: %v", err)
	}
	var names []string
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, current := range scopeMetrics.Metrics {
			names = append(names, current.Name)
			histogram, ok := current.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s aggregation type = %T, want float64 histogram", current.Name, current.Data)
			}
			for _, point := range histogram.DataPoints {
				for _, forbidden := range []attribute.Key{
					"reason", "url.template", "url.path", "url.full",
					"user.id", "enduser.id",
				} {
					if point.Attributes.HasValue(forbidden) {
						t.Errorf("%s contains forbidden attribute %q", current.Name, forbidden)
					}
				}
			}
		}
	}
	slices.Sort(names)
	wantNames := []string{"http.client.request.duration", "http.server.request.duration"}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("metric names = %v, want duration-only set %v", names, wantNames)
	}
	for _, name := range names {
		if strings.Contains(name, "active_requests") || strings.Contains(name, "body.size") || strings.Contains(name, "requests_total") {
			t.Errorf("unexpected opt-in or legacy metric %q", name)
		}
	}
}

func TestHTTPDurationCarriesSampledTraceExemplar(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithExemplarFilter(exemplar.TraceBasedFilter),
	)
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("MeterProvider.Shutdown() failed: %v", err)
		}
	})
	wrapper, err := NewHTTPClientWrapper(provider)
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody}, nil
	}))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	ctx, span := tracerProvider.Tracer("test").Start(t.Context(), "request")
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() failed: %v", err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatalf("RoundTrip() failed: %v", err)
	}
	_ = response.Body.Close()
	spanContext := span.SpanContext()
	span.End()
	observation := collectSingleHistogram(t, reader, "http.client.request.duration", []attribute.KeyValue{
		attribute.String("http.request.method", "GET"),
		attribute.String("server.address", "example.test"),
		attribute.Int("server.port", 80),
		attribute.Int("http.response.status_code", 200),
		attribute.String("network.protocol.version", "1.1"),
	})
	if len(observation.point.Exemplars) != 1 {
		t.Fatalf("exemplar count = %d, want 1", len(observation.point.Exemplars))
	}
	exemplar := observation.point.Exemplars[0]
	traceID := spanContext.TraceID()
	spanID := spanContext.SpanID()
	if !slices.Equal(exemplar.TraceID, traceID[:]) || !slices.Equal(exemplar.SpanID, spanID[:]) {
		t.Fatalf("exemplar IDs = trace %x span %x, want trace %x span %x", exemplar.TraceID, exemplar.SpanID, traceID, spanID)
	}
}

func TestHTTPNoopFastPathsAllocateNothing(t *testing.T) {
	filter, err := NewHTTPServerFilter(noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("NewHTTPServerFilter() failed: %v", err)
	}
	handler := filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	writer := &bareResponseWriter{header: make(http.Header)}
	request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if allocs := testing.AllocsPerRun(1_000, func() { handler.ServeHTTP(writer, request) }); allocs != 0 {
		t.Fatalf("noop server allocations/request = %v, want 0", allocs)
	}

	wrapper, err := NewHTTPClientWrapper(noop.NewMeterProvider())
	if err != nil {
		t.Fatalf("NewHTTPClientWrapper() failed: %v", err)
	}
	baseResponse := &http.Response{StatusCode: 200, Body: http.NoBody}
	transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) { return baseResponse, nil }))
	if err != nil {
		t.Fatalf("wrapper(base) failed: %v", err)
	}
	clientRequest, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
	if err != nil {
		t.Fatalf("http.NewRequest() failed: %v", err)
	}
	if allocs := testing.AllocsPerRun(1_000, func() {
		response, roundTripErr := transport.RoundTrip(clientRequest)
		if roundTripErr != nil {
			panic(roundTripErr)
		}
		_ = response.Body.Close()
	}); allocs != 0 {
		t.Fatalf("noop client allocations/request = %v, want 0", allocs)
	}
}

func BenchmarkHTTPServerFilter(b *testing.B) {
	benchmarks := []struct {
		name     string
		provider metric.MeterProvider
	}{
		{name: "noop", provider: noop.NewMeterProvider()},
		{name: "enabled", provider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			filter, err := NewHTTPServerFilter(benchmark.provider)
			if err != nil {
				b.Fatalf("NewHTTPServerFilter() failed: %v", err)
			}
			handler := filter(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			writer := &bareResponseWriter{header: make(http.Header)}
			request := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
			b.ReportAllocs()
			for b.Loop() {
				handler.ServeHTTP(writer, request)
			}
		})
	}
}

func BenchmarkHTTPClientWrapper(b *testing.B) {
	benchmarks := []struct {
		name     string
		provider metric.MeterProvider
	}{
		{name: "noop", provider: noop.NewMeterProvider()},
		{name: "enabled", provider: sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))},
	}
	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			wrapper, err := NewHTTPClientWrapper(benchmark.provider)
			if err != nil {
				b.Fatalf("NewHTTPClientWrapper() failed: %v", err)
			}
			response := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Body: http.NoBody}
			transport, err := wrapper(roundTripperFunc(func(*http.Request) (*http.Response, error) { return response, nil }))
			if err != nil {
				b.Fatalf("wrapper(base) failed: %v", err)
			}
			request, err := http.NewRequest(http.MethodGet, "http://example.test/", nil)
			if err != nil {
				b.Fatalf("http.NewRequest() failed: %v", err)
			}
			b.ReportAllocs()
			for b.Loop() {
				response, roundTripErr := transport.RoundTrip(request)
				if roundTripErr != nil {
					b.Fatal(roundTripErr)
				}
				_ = response.Body.Close()
			}
		})
	}
}

type histogramObservation struct {
	metric metricdata.Metrics
	point  metricdata.HistogramDataPoint[float64]
	scope  struct {
		name      string
		schemaURL string
	}
}

func newTestMeterProvider(t *testing.T) (*sdkmetric.ManualReader, *sdkmetric.MeterProvider) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("MeterProvider.Shutdown() failed: %v", err)
		}
	})
	return reader, provider
}

func collectSingleHistogram(t *testing.T, reader *sdkmetric.ManualReader, name string, wantAttrs []attribute.KeyValue) histogramObservation {
	t.Helper()
	observations := collectHistograms(t, reader, name)
	wantSet := attribute.NewSet(wantAttrs...)
	if len(observations) != 1 {
		t.Fatalf("%s data point count = %d, want 1; observations = %#v", name, len(observations), observations)
	}
	if !observations[0].point.Attributes.Equals(&wantSet) {
		t.Fatalf("%s attributes = %v, want exactly %v", name, observations[0].point.Attributes.ToSlice(), wantSet.ToSlice())
	}
	return observations[0]
}

func collectHistograms(t *testing.T, reader *sdkmetric.ManualReader, name string) []histogramObservation {
	t.Helper()
	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resourceMetrics); err != nil {
		t.Fatalf("ManualReader.Collect() failed: %v", err)
	}
	var observations []histogramObservation
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, current := range scopeMetrics.Metrics {
			if current.Name != name {
				continue
			}
			histogram, ok := current.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s aggregation type = %T, want metricdata.Histogram[float64]", name, current.Data)
			}
			for _, point := range histogram.DataPoints {
				observation := histogramObservation{metric: current, point: point}
				observation.scope.name = scopeMetrics.Scope.Name
				observation.scope.schemaURL = scopeMetrics.Scope.SchemaURL
				observations = append(observations, observation)
			}
		}
	}
	return observations
}

func histogramCount(t *testing.T, reader *sdkmetric.ManualReader, name string) uint64 {
	t.Helper()
	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resourceMetrics); err != nil {
		t.Fatalf("ManualReader.Collect() failed: %v", err)
	}
	var count uint64
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, current := range scopeMetrics.Metrics {
			if current.Name != name {
				continue
			}
			histogram, ok := current.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s aggregation type = %T, want metricdata.Histogram[float64]", name, current.Data)
			}
			for _, point := range histogram.DataPoints {
				count += point.Count
			}
		}
	}
	return count
}

func assertDurationInstrument(t *testing.T, observation histogramObservation) {
	t.Helper()
	if observation.metric.Unit != "s" {
		t.Errorf("metric unit = %q, want s", observation.metric.Unit)
	}
	if observation.scope.name != instrumentationName {
		t.Errorf("scope name = %q, want %q", observation.scope.name, instrumentationName)
	}
	if observation.scope.schemaURL != semconv.SchemaURL {
		t.Errorf("scope schema URL = %q, want %q", observation.scope.schemaURL, semconv.SchemaURL)
	}
	if observation.point.Count != 1 {
		t.Errorf("histogram count = %d, want 1", observation.point.Count)
	}
	if observation.point.Sum < 0 {
		t.Errorf("histogram sum = %v, want non-negative", observation.point.Sum)
	}
	if !slices.Equal(observation.point.Bounds, durationBounds) {
		t.Errorf("histogram bounds = %v, want %v", observation.point.Bounds, durationBounds)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type staticDiscovery struct {
	instances []*registry.ServiceInstance
}

func (discovery staticDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return discovery.instances, nil
}

func (discovery staticDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	ctx, cancel := context.WithCancel(ctx)
	return &staticWatcher{ctx: ctx, cancel: cancel, instances: discovery.instances}, nil
}

type staticWatcher struct {
	ctx       context.Context
	cancel    context.CancelFunc
	instances []*registry.ServiceInstance
	delivered bool
}

func (watcher *staticWatcher) Next() ([]*registry.ServiceInstance, error) {
	if !watcher.delivered {
		watcher.delivered = true
		return watcher.instances, nil
	}
	<-watcher.ctx.Done()
	return nil, watcher.ctx.Err()
}

func (watcher *staticWatcher) Stop() error {
	watcher.cancel()
	return nil
}

type failingMeterProvider struct {
	metric.MeterProvider
	err error
}

func (provider failingMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return failingMeter{err: provider.err}
}

type failingMeter struct {
	metric.Meter
	err error
}

func (meter failingMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, meter.err
}

type nilMeterProvider struct {
	metric.MeterProvider
}

func (nilMeterProvider) Meter(string, ...metric.MeterOption) metric.Meter { return nil }

type nilInstrumentProvider struct {
	metric.MeterProvider
}

func (nilInstrumentProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	return nilInstrumentMeter{}
}

type nilInstrumentMeter struct {
	metric.Meter
}

func (nilInstrumentMeter) Float64Histogram(string, ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	return nil, nil
}

type writeFailure struct{}

func (writeFailure) Error() string { return "sensitive write details" }

type hijackFailure struct{}

func (hijackFailure) Error() string { return "sensitive hijack details" }

type transportFailure struct{}

func (transportFailure) Error() string { return "sensitive transport details" }

type bareResponseWriter struct {
	header http.Header
}

func (writer *bareResponseWriter) Header() http.Header     { return writer.header }
func (*bareResponseWriter) Write(body []byte) (int, error) { return len(body), nil }
func (*bareResponseWriter) WriteHeader(int)                {}

type errorResponseWriter struct {
	header http.Header
	err    error
}

func (writer *errorResponseWriter) Header() http.Header       { return writer.header }
func (writer *errorResponseWriter) Write([]byte) (int, error) { return 0, writer.err }
func (*errorResponseWriter) WriteHeader(int)                  {}

type fullResponseWriter struct {
	bareResponseWriter
}

func newFullResponseWriter() *fullResponseWriter {
	return &fullResponseWriter{bareResponseWriter: bareResponseWriter{header: make(http.Header)}}
}

func (*fullResponseWriter) Flush()                               {}
func (*fullResponseWriter) FlushError() error                    { return nil }
func (*fullResponseWriter) Push(string, *http.PushOptions) error { return nil }
func (*fullResponseWriter) SetReadDeadline(time.Time) error      { return nil }
func (*fullResponseWriter) SetWriteDeadline(time.Time) error     { return nil }
func (*fullResponseWriter) EnableFullDuplex() error              { return nil }
func (writer *fullResponseWriter) ReadFrom(src io.Reader) (int64, error) {
	return io.Copy(io.Discard, src)
}
func (*fullResponseWriter) WriteString(value string) (int, error) { return len(value), nil }
func (*fullResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	server, peer := net.Pipe()
	_ = peer.Close()
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

type hijackResponseWriter struct {
	header http.Header
	err    error
	peer   net.Conn
}

func (writer *hijackResponseWriter) Header() http.Header     { return writer.header }
func (*hijackResponseWriter) Write(body []byte) (int, error) { return len(body), nil }
func (*hijackResponseWriter) WriteHeader(int)                {}
func (writer *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if writer.err != nil {
		return nil, nil, writer.err
	}
	server, peer := net.Pipe()
	writer.peer = peer
	return server, bufio.NewReadWriter(bufio.NewReader(server), bufio.NewWriter(server)), nil
}

func (writer *hijackResponseWriter) close() {
	if writer.peer != nil {
		_ = writer.peer.Close()
	}
}

type plainReader struct {
	data []byte
}

func (reader *plainReader) Read(target []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	n := copy(target, reader.data)
	reader.data = reader.data[n:]
	return n, nil
}

type countingBody struct {
	reads int
}

func (body *countingBody) Read([]byte) (int, error) {
	body.reads++
	return 0, io.EOF
}

func (*countingBody) Close() error { return nil }

type gatedRequestBody struct {
	started chan struct{}
	release chan struct{}
}

func (body *gatedRequestBody) Read([]byte) (int, error) {
	close(body.started)
	<-body.release
	return 0, io.EOF
}

func (*gatedRequestBody) Close() error { return nil }
