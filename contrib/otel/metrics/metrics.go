// Package metrics instruments OpenKratos HTTP transports with OpenTelemetry
// semantic-convention metrics.
package metrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/felixge/httpsnoop"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/semconv/v1.41.0/httpconv"
	"golang.org/x/net/http/httpguts"

	kratostransport "github.com/openkratos/kratos/transport"
	kratoshttp "github.com/openkratos/kratos/transport/http"
)

const (
	instrumentationName = "github.com/openkratos/kratos/contrib/otel/metrics"
	httpScheme          = "http"
	httpsScheme         = "https"
	defaultHTTPPort     = 80
	defaultHTTPSPort    = 443
)

var defaultKnownMethods = []string{
	http.MethodConnect,
	http.MethodDelete,
	http.MethodGet,
	http.MethodHead,
	http.MethodOptions,
	http.MethodPatch,
	http.MethodPost,
	http.MethodPut,
	http.MethodTrace,
	"QUERY",
}

// HTTPServerOption configures HTTP server metric instrumentation.
type HTTPServerOption func(*httpServerConfig) error

// HTTPClientOption configures HTTP client metric instrumentation.
type HTTPClientOption func(*httpClientConfig) error

type httpServerConfig struct {
	knownMethods map[string]httpconv.RequestMethodAttr
}

type httpClientConfig struct {
	knownMethods map[string]httpconv.RequestMethodAttr
}

// WithHTTPServerKnownMethods replaces the HTTP methods known by server
// instrumentation. Methods are case-sensitive. Unknown methods are recorded as
// _OTHER. Passing no methods makes every method unknown.
func WithHTTPServerKnownMethods(methods ...string) HTTPServerOption {
	methods = append([]string(nil), methods...)
	return func(config *httpServerConfig) error {
		known, err := knownMethods(methods)
		if err != nil {
			return err
		}
		config.knownMethods = known
		return nil
	}
}

// WithHTTPClientKnownMethods replaces the HTTP methods known by client
// instrumentation. Methods are case-sensitive. Unknown methods are recorded as
// _OTHER. Passing no methods makes every method unknown.
func WithHTTPClientKnownMethods(methods ...string) HTTPClientOption {
	methods = append([]string(nil), methods...)
	return func(config *httpClientConfig) error {
		known, err := knownMethods(methods)
		if err != nil {
			return err
		}
		config.knownMethods = known
		return nil
	}
}

// NewHTTPServerFilter returns an HTTP filter that records
// http.server.request.duration over the full ServeHTTP lifecycle.
func NewHTTPServerFilter(provider metric.MeterProvider, opts ...HTTPServerOption) (kratoshttp.FilterFunc, error) {
	if isNil(provider) {
		return nil, errors.New("metrics: HTTP server MeterProvider is nil")
	}
	known, err := knownMethods(defaultKnownMethods)
	if err != nil {
		return nil, fmt.Errorf("metrics: configure default HTTP server methods: %w", err)
	}
	config := httpServerConfig{knownMethods: known}
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("metrics: HTTP server option %d is nil", i)
		}
		if optionErr := opt(&config); optionErr != nil {
			return nil, fmt.Errorf("metrics: apply HTTP server option %d: %w", i, optionErr)
		}
	}

	meter := provider.Meter(
		instrumentationName,
		metric.WithSchemaURL(semconv.SchemaURL),
	)
	if isNil(meter) {
		return nil, errors.New("metrics: HTTP server MeterProvider returned a nil Meter")
	}
	duration, err := httpconv.NewServerRequestDuration(meter)
	if err != nil {
		return nil, fmt.Errorf("metrics: create http.server.request.duration: %w", err)
	}
	if isNil(duration.Inst()) {
		return nil, errors.New("metrics: create http.server.request.duration: Meter returned a nil instrument")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			ctx := request.Context()
			if !duration.Inst().Enabled(ctx) {
				next.ServeHTTP(writer, request)
				return
			}

			start := time.Now()
			state := responseState{upgradeRequest: isUpgradeRequest(request)}
			wrapped := httpsnoop.Wrap(writer, state.hooks())
			defer func() {
				panicValue := recover()
				if panicValue != nil {
					recordServerDuration(duration, config.knownMethods, request, state.panicSnapshot(), time.Since(start))
					panic(panicValue)
				}
				recordServerDuration(duration, config.knownMethods, request, state.normalSnapshot(), time.Since(start))
			}()
			next.ServeHTTP(wrapped, request)
		})
	}, nil
}

// NewHTTPClientWrapper returns a transport decorator that records
// http.client.request.duration until response headers or a transport error are
// returned.
func NewHTTPClientWrapper(provider metric.MeterProvider, opts ...HTTPClientOption) (kratoshttp.RoundTripperWrapper, error) {
	if isNil(provider) {
		return nil, errors.New("metrics: HTTP client MeterProvider is nil")
	}
	known, err := knownMethods(defaultKnownMethods)
	if err != nil {
		return nil, fmt.Errorf("metrics: configure default HTTP client methods: %w", err)
	}
	config := httpClientConfig{knownMethods: known}
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("metrics: HTTP client option %d is nil", i)
		}
		if optionErr := opt(&config); optionErr != nil {
			return nil, fmt.Errorf("metrics: apply HTTP client option %d: %w", i, optionErr)
		}
	}

	meter := provider.Meter(
		instrumentationName,
		metric.WithSchemaURL(semconv.SchemaURL),
	)
	if isNil(meter) {
		return nil, errors.New("metrics: HTTP client MeterProvider returned a nil Meter")
	}
	duration, err := httpconv.NewClientRequestDuration(meter)
	if err != nil {
		return nil, fmt.Errorf("metrics: create http.client.request.duration: %w", err)
	}
	if isNil(duration.Inst()) {
		return nil, errors.New("metrics: create http.client.request.duration: Meter returned a nil instrument")
	}

	return func(base http.RoundTripper) (http.RoundTripper, error) {
		if isNil(base) {
			return nil, errors.New("metrics: HTTP client base RoundTripper is nil")
		}
		return &metricRoundTripper{
			base:         base,
			duration:     duration,
			knownMethods: config.knownMethods,
		}, nil
	}, nil
}

type metricRoundTripper struct {
	base         http.RoundTripper
	duration     httpconv.ClientRequestDuration
	knownMethods map[string]httpconv.RequestMethodAttr
}

func (transport *metricRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || !transport.duration.Inst().Enabled(request.Context()) {
		return transport.base.RoundTrip(request)
	}
	ctx := request.Context()
	method := requestMethod(transport.knownMethods, request.Method)
	address, port := serverAddressPort(request)
	start := time.Now()
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			recordClientDuration(
				ctx,
				transport.duration,
				method,
				address,
				port,
				nil,
				nil,
				time.Since(start),
				true,
			)
			panic(panicValue)
		}
	}()
	response, err := transport.base.RoundTrip(request)
	recordClientDuration(ctx, transport.duration, method, address, port, response, err, time.Since(start), false)
	return response, err
}

type responseSnapshot struct {
	statusCode int
	writeErr   error
	panicked   bool
}

type responseState struct {
	mu             sync.Mutex
	statusCode     int
	writeErr       error
	hijacked       bool
	upgradeRequest bool
}

func (state *responseState) hooks() httpsnoop.Hooks {
	return httpsnoop.Hooks{
		WriteHeader: func(next httpsnoop.WriteHeaderFunc) httpsnoop.WriteHeaderFunc {
			return func(code int) {
				next(code)
				state.commit(code)
			}
		},
		Write: func(next httpsnoop.WriteFunc) httpsnoop.WriteFunc {
			return func(body []byte) (int, error) {
				state.commit(http.StatusOK)
				n, err := next(body)
				state.fail(err)
				return n, err
			}
		},
		WriteString: func(next httpsnoop.WriteStringFunc) httpsnoop.WriteStringFunc {
			return func(body string) (int, error) {
				state.commit(http.StatusOK)
				n, err := next(body)
				state.fail(err)
				return n, err
			}
		},
		ReadFrom: func(next httpsnoop.ReadFromFunc) httpsnoop.ReadFromFunc {
			return func(src io.Reader) (int64, error) {
				state.commit(http.StatusOK)
				n, err := next(src)
				state.fail(err)
				return n, err
			}
		},
		Flush: func(next httpsnoop.FlushFunc) httpsnoop.FlushFunc {
			return func() {
				state.commit(http.StatusOK)
				next()
			}
		},
		FlushError: func(next httpsnoop.FlushErrorFunc) httpsnoop.FlushErrorFunc {
			return func() error {
				state.commit(http.StatusOK)
				err := next()
				state.fail(err)
				return err
			}
		},
		Hijack: func(next httpsnoop.HijackFunc) httpsnoop.HijackFunc {
			return func() (conn net.Conn, rw *bufio.ReadWriter, err error) {
				conn, rw, err = next()
				if err == nil {
					state.completeHijack()
				} else {
					state.fail(err)
				}
				return conn, rw, err
			}
		},
	}
}

func (state *responseState) commit(code int) {
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		return
	}
	state.mu.Lock()
	if state.statusCode == 0 {
		state.statusCode = code
	}
	state.mu.Unlock()
}

func (state *responseState) fail(err error) {
	if err == nil {
		return
	}
	state.mu.Lock()
	if state.writeErr == nil {
		state.writeErr = err
	}
	state.mu.Unlock()
}

func (state *responseState) completeHijack() {
	state.mu.Lock()
	state.hijacked = true
	if state.upgradeRequest && state.statusCode == 0 {
		state.statusCode = http.StatusSwitchingProtocols
	}
	state.mu.Unlock()
}

func (state *responseState) normalSnapshot() responseSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.statusCode == 0 && !state.hijacked {
		state.statusCode = http.StatusOK
	}
	return responseSnapshot{statusCode: state.statusCode, writeErr: state.writeErr}
}

func (state *responseState) panicSnapshot() responseSnapshot {
	state.mu.Lock()
	defer state.mu.Unlock()
	return responseSnapshot{statusCode: state.statusCode, writeErr: state.writeErr, panicked: true}
}

func recordServerDuration(
	duration httpconv.ServerRequestDuration,
	known map[string]httpconv.RequestMethodAttr,
	request *http.Request,
	response responseSnapshot,
	elapsed time.Duration,
) {
	// Response attributes become available only after the handler completes.
	attrs := make([]attribute.KeyValue, 0, 4)
	if response.statusCode > 0 {
		attrs = append(attrs, duration.AttrResponseStatusCode(response.statusCode))
	}
	if route := requestRoute(request.Pattern); route != "" {
		attrs = append(attrs, duration.AttrRoute(route))
	}
	if version := protocolVersion(request.ProtoMajor, request.ProtoMinor); version != "" {
		attrs = append(attrs, duration.AttrNetworkProtocolVersion(version))
	}
	switch {
	case response.panicked:
		attrs = append(attrs, duration.AttrErrorType(httpconv.ErrorTypeOther))
	case response.statusCode >= 500 && response.statusCode <= 599:
		attrs = append(attrs, duration.AttrErrorType(httpconv.ErrorTypeAttr(strconv.Itoa(response.statusCode))))
	case response.writeErr != nil:
		attrs = append(attrs, semconv.ErrorType(response.writeErr))
	}
	duration.Record(
		request.Context(),
		elapsed.Seconds(),
		requestMethod(known, request.Method),
		serverScheme(request),
		attrs...,
	)
}

func recordClientDuration(
	ctx context.Context,
	duration httpconv.ClientRequestDuration,
	method httpconv.RequestMethodAttr,
	address string,
	port int,
	response *http.Response,
	err error,
	elapsed time.Duration,
	panicked bool,
) {
	// Response attributes become available only after the base transport returns.
	attrs := make([]attribute.KeyValue, 0, 3)
	if response != nil {
		if response.StatusCode > 0 {
			attrs = append(attrs, duration.AttrResponseStatusCode(response.StatusCode))
		}
		if version := protocolVersion(response.ProtoMajor, response.ProtoMinor); version != "" {
			attrs = append(attrs, duration.AttrNetworkProtocolVersion(version))
		}
	}
	switch {
	case panicked:
		attrs = append(attrs, duration.AttrErrorType(httpconv.ErrorTypeOther))
	case err != nil:
		attrs = append(attrs, semconv.ErrorType(err))
	case response != nil && response.StatusCode >= 400 && response.StatusCode <= 599:
		attrs = append(attrs, duration.AttrErrorType(httpconv.ErrorTypeAttr(strconv.Itoa(response.StatusCode))))
	}
	duration.Record(
		ctx,
		elapsed.Seconds(),
		method,
		address,
		port,
		attrs...,
	)
}

func knownMethods(methods []string) (map[string]httpconv.RequestMethodAttr, error) {
	known := make(map[string]httpconv.RequestMethodAttr, len(methods))
	for i, method := range methods {
		if !httpguts.ValidHeaderFieldName(method) {
			return nil, fmt.Errorf("known HTTP method %d %q is not a valid token", i, method)
		}
		known[method] = httpconv.RequestMethodAttr(method)
	}
	return known, nil
}

func requestMethod(known map[string]httpconv.RequestMethodAttr, method string) httpconv.RequestMethodAttr {
	if value, ok := known[method]; ok {
		return value
	}
	return httpconv.RequestMethodOther
}

func requestRoute(pattern string) string {
	if strings.Contains(pattern, "{__openkratos") {
		return ""
	}
	start := strings.IndexByte(pattern, '/')
	if start < 0 {
		return ""
	}
	return pattern[start:]
}

func serverScheme(request *http.Request) string {
	if request.TLS != nil {
		return httpsScheme
	}
	return httpScheme
}

func serverAddressPort(request *http.Request) (string, int) {
	var authority, scheme string
	if request.URL != nil {
		authority = request.URL.Host
		scheme = request.URL.Scheme
	}
	if logical, ok := discoveryLogicalAuthority(request); ok {
		authority = logical
	}
	target := url.URL{Host: authority}
	address := target.Hostname()
	portText := target.Port()
	if portText != "" {
		port, err := strconv.Atoi(portText)
		if err == nil && port >= 0 && port <= 65535 {
			return address, port
		}
		return address, 0
	}
	switch strings.ToLower(scheme) {
	case httpScheme:
		return address, defaultHTTPPort
	case httpsScheme:
		return address, defaultHTTPSPort
	default:
		return address, 0
	}
}

func discoveryLogicalAuthority(request *http.Request) (string, bool) {
	// Redirect requests inherit their parent's context. Their own URL is the
	// logical target for that RoundTrip, so only the initial request may use the
	// OpenKratos discovery endpoint.
	if request.Response != nil {
		return "", false
	}
	info, ok := kratostransport.FromClientContext(request.Context())
	if !ok || isNil(info) || info.Kind() != kratostransport.KindHTTP {
		return "", false
	}
	endpoint, err := url.Parse(info.Endpoint())
	if err != nil || !strings.EqualFold(endpoint.Scheme, "discovery") {
		return "", false
	}
	service := strings.TrimPrefix(endpoint.Path, "/")
	if service == "" {
		service = endpoint.Host
	}
	return service, service != ""
}

func protocolVersion(major, minor int) string {
	if major <= 0 || minor < 0 {
		return ""
	}
	if major >= 2 && minor == 0 {
		return strconv.Itoa(major)
	}
	return strconv.Itoa(major) + "." + strconv.Itoa(minor)
}

func isUpgradeRequest(request *http.Request) bool {
	return request.Method != http.MethodConnect &&
		request.ProtoMajor == 1 && request.ProtoMinor >= 1 &&
		hasValidUpgradeProtocol(request.Header.Values("Upgrade")) &&
		httpguts.HeaderValuesContainsToken(request.Header.Values("Connection"), "upgrade")
}

func hasValidUpgradeProtocol(values []string) bool {
	var found bool
	for _, value := range values {
		for protocol := range strings.SplitSeq(value, ",") {
			protocol = strings.TrimSpace(protocol)
			if protocol == "" {
				continue
			}
			name, version, hasVersion := strings.Cut(protocol, "/")
			if !httpguts.ValidHeaderFieldName(name) ||
				hasVersion && !httpguts.ValidHeaderFieldName(version) {
				return false
			}
			found = true
		}
	}
	return found
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
