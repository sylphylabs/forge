package http

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/types/known/emptypb"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/wrr"
	"github.com/sylphylabs/forge/transport"
)

type mockRoundTripper struct{}

func (rt *mockRoundTripper) RoundTrip(_ *http.Request) (resp *http.Response, err error) {
	return
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type captureRoundTripper struct {
	req *http.Request
}

func (rt *captureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.req = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/protojson"}},
		Body:       io.NopCloser(bytes.NewBufferString("{}")),
	}, nil
}

type mockCallOption struct {
	needErr bool
}

func (x *mockCallOption) before(_ *callInfo) error {
	if x.needErr {
		return errors.New("option need return err")
	}
	return nil
}

func (x *mockCallOption) after(_ *callInfo, _ *csAttempt) {
	log.Println("run in mockCallOption.after")
}

func TestWithSubset(t *testing.T) {
	co := &clientOptions{}
	o := WithSubset(1)
	o(co)
	if co.subsetSize != 1 {
		t.Error("expected subset size to be 1")
	}
}

func TestWithTransport(t *testing.T) {
	ov := &mockRoundTripper{}
	o := WithTransport(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.transport, ov) {
		t.Errorf("expected transport to be %v, got %v", ov, co.transport)
	}
}

func TestWithRoundTripperWrapperAppends(t *testing.T) {
	options := new(clientOptions)
	first := RoundTripperWrapper(func(next http.RoundTripper) (http.RoundTripper, error) {
		return next, nil
	})
	second := RoundTripperWrapper(func(next http.RoundTripper) (http.RoundTripper, error) {
		return next, nil
	})

	WithRoundTripperWrapper(first)(options)
	WithRoundTripperWrapper(second)(options)

	if got := len(options.roundTripperWrappers); got != 2 {
		t.Fatalf("wrapper count = %d, want 2", got)
	}
}

func TestWithTimeout(t *testing.T) {
	ov := 1 * time.Second
	o := WithTimeout(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.timeout, ov) {
		t.Errorf("expected timeout to be %v, got %v", ov, co.timeout)
	}
}

func TestWithBlock(t *testing.T) {
	o := WithBlock()
	co := &clientOptions{}
	o(co)
	if !co.block {
		t.Errorf("expected block to be true, got %v", co.block)
	}
}

func TestWithTLSConfig(t *testing.T) {
	ov := &tls.Config{}
	o := WithTLSConfig(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.tlsConf, ov) {
		t.Errorf("expected tls config to be %v, got %v", ov, co.tlsConf)
	}
}

func TestWithUserAgent(t *testing.T) {
	ov := "forge"
	o := WithUserAgent(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.userAgent, ov) {
		t.Errorf("expected user agent to be %v, got %v", ov, co.userAgent)
	}
}

func TestWithMiddleware(t *testing.T) {
	o := &clientOptions{}
	v := []middleware.UnaryMiddleware{
		func(middleware.UnaryHandler) middleware.UnaryHandler { return nil },
	}
	WithMiddleware(v...)(o)
	if !reflect.DeepEqual(o.middleware, v) {
		t.Errorf("expected middleware to be %v, got %v", v, o.middleware)
	}
}

func TestWithEndpoint(t *testing.T) {
	ov := "some-endpoint"
	o := WithEndpoint(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.endpoint, ov) {
		t.Errorf("expected endpoint to be %v, got %v", ov, co.endpoint)
	}
}

func TestWithRequestEncoder(t *testing.T) {
	o := &clientOptions{}
	v := func(context.Context, string, any) (body []byte, err error) {
		return nil, nil
	}
	WithRequestEncoder(v)(o)
	if o.encoder == nil {
		t.Errorf("expected encoder to be not nil")
	}
}

func TestWithResponseDecoder(t *testing.T) {
	o := &clientOptions{}
	v := func(context.Context, *http.Response, any) error { return nil }
	WithResponseDecoder(v)(o)
	if o.decoder == nil {
		t.Errorf("expected encoder to be not nil")
	}
}

func TestWithErrorDecoder(t *testing.T) {
	o := &clientOptions{}
	v := func(context.Context, *http.Response) error { return nil }
	WithErrorDecoder(v)(o)
	if o.errorDecoder == nil {
		t.Errorf("expected encoder to be not nil")
	}
}

type mockDiscovery struct{}

func (*mockDiscovery) GetService(_ context.Context, _ string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (*mockDiscovery) Watch(_ context.Context, _ string) (registry.Watcher, error) {
	return &mockWatcher{}, nil
}

type mockWatcher struct{}

func (m *mockWatcher) Next() ([]*registry.ServiceInstance, error) {
	instance := &registry.ServiceInstance{
		ID:        "1",
		Name:      "forge",
		Version:   "v1",
		Metadata:  map[string]string{},
		Endpoints: []string{fmt.Sprintf("http://127.0.0.1:9001?isSecure=%s", strconv.FormatBool(false))},
	}
	time.Sleep(time.Millisecond * 500)
	return []*registry.ServiceInstance{instance}, nil
}

func (*mockWatcher) Stop() error {
	return nil
}

func TestWithDiscovery(t *testing.T) {
	ov := &mockDiscovery{}
	o := WithDiscovery(ov)
	co := &clientOptions{}
	o(co)
	if !reflect.DeepEqual(co.discovery, ov) {
		t.Errorf("expected discovery to be %v, got %v", ov, co.discovery)
	}
}

func TestWithNodeFilter(t *testing.T) {
	ov := func(context.Context, []selector.Node) []selector.Node {
		return []selector.Node{&selector.DefaultNode{}}
	}
	o := WithNodeFilter(ov)
	co := &clientOptions{}
	o(co)
	for _, n := range co.nodeFilters {
		ret := n(context.Background(), nil)
		if len(ret) != 1 {
			t.Errorf("expected node  length to be 1, got %v", len(ret))
		}
	}
}

// countingBuilder records how many selectors it built, so a test can tell
// whether a client consulted the policy it was given.
type countingBuilder struct {
	built int
	inner selector.Builder
}

func (b *countingBuilder) Build() selector.Selector {
	b.built++
	return b.inner.Build()
}

func TestNewClientUsesConfiguredSelector(t *testing.T) {
	configured := &countingBuilder{inner: wrr.NewBuilder()}
	client, err := NewClient(t.Context(), WithSelector(configured))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if configured.built != 1 {
		t.Fatalf("configured selector built %d times, want 1", configured.built)
	}
}

// TestNewClientSelectorsAreIndependent proves two clients in one process
// balance with their own policy rather than a shared one.
func TestNewClientSelectorsAreIndependent(t *testing.T) {
	first := &countingBuilder{inner: wrr.NewBuilder()}
	second := &countingBuilder{inner: wrr.NewBuilder()}

	for _, b := range []*countingBuilder{first, second} {
		client, err := NewClient(t.Context(), WithSelector(b))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
	}

	if first.built != 1 || second.built != 1 {
		t.Fatalf("built first=%d second=%d, want 1 each", first.built, second.built)
	}
	if first.inner == second.inner {
		t.Fatal("expected two independent policies")
	}
}

func TestNewClientRejectsNilSelector(t *testing.T) {
	if _, err := NewClient(t.Context(), WithSelector(nil)); err == nil {
		t.Fatal("expected an error for a nil selector builder, got nil")
	}
}

// TestNewClientDefaultsToWRR pins the default policy to the package rather
// than to whichever transport happened to be linked first.
func TestNewClientDefaultsToWRR(t *testing.T) {
	client, err := NewClient(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	if client.selector == nil {
		t.Fatal("expected a default selector, got nil")
	}
}

func TestDefaultRequestEncoder(t *testing.T) {
	r, _ := http.NewRequest(http.MethodPost, "", io.NopCloser(bytes.NewBufferString(`{"a":"1", "b": 2}`)))
	r.Header.Set("Content-Type", "application/xml")

	v1 := &struct {
		A string `json:"a"`
		B int64  `json:"b"`
	}{"a", 1}
	b, err := DefaultRequestEncoder(context.TODO(), "application/json", v1)
	if err != nil {
		t.Fatal(err)
	}
	v1b := &struct {
		A string `json:"a"`
		B int64  `json:"b"`
	}{}
	err = json.Unmarshal(b, v1b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v1b, v1) {
		t.Errorf("expected %v, got %v", v1, v1b)
	}
}

func TestDefaultRequestEncoderHTTPBody(t *testing.T) {
	body := &httpbody.HttpBody{Data: []byte("raw request")}
	got, err := DefaultRequestEncoder(context.TODO(), "application/octet-stream", body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "raw request" {
		t.Errorf("expected %v, got %v", "raw request", string(got))
	}
}

func TestDefaultRequestEncoderUnknownCodec(t *testing.T) {
	_, err := DefaultRequestEncoder(context.TODO(), "application/x-unknown", &struct{}{})
	if err == nil {
		t.Fatal("expected error")
	}
	se := new(forgeerrors.Error)
	if !errors.As(err, &se) {
		t.Fatalf("expected forge error, got %T", err)
	}
	if se.Reason() != "CODEC" {
		t.Errorf("expected %v, got %v", "CODEC", se.Reason())
	}
}

func TestInvokeAcceptHeader(t *testing.T) {
	rt := &captureRoundTripper{}
	client, err := NewClient(context.Background(), WithEndpoint("127.0.0.1:8888"), WithTransport(rt))
	if err != nil {
		t.Fatal(err)
	}
	err = client.Invoke(
		context.Background(),
		http.MethodPost,
		"/go",
		&emptypb.Empty{},
		&emptypb.Empty{},
		Accept("application/protojson"),
		ContentType("application/protojson"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := rt.req.Header.Get("Accept"); got != "application/protojson" {
		t.Errorf("expected %v got %v", "application/protojson", got)
	}
	if got := rt.req.Header.Get("Content-Type"); got != "application/protojson" {
		t.Errorf("expected %v got %v", "application/protojson", got)
	}
}

func TestInvokePreservesEndpointBasePath(t *testing.T) {
	rt := &captureRoundTripper{}
	client, err := NewClient(t.Context(),
		WithEndpoint("https://api.example.com/base/v1/"),
		WithTransport(rt),
	)
	if err != nil {
		t.Fatal(err)
	}
	err = client.Invoke(
		t.Context(),
		http.MethodGet,
		"/greeters/a%2Fb?view=full",
		nil,
		&emptypb.Empty{},
		Accept("application/protojson"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := rt.req.URL.EscapedPath(), "/base/v1/greeters/a%2Fb"; got != want {
		t.Fatalf("request path = %q, want %q", got, want)
	}
	if got := rt.req.URL.RawQuery; got != "view=full" {
		t.Fatalf("request query = %q, want %q", got, "view=full")
	}
}

func TestDefaultResponseDecoder(t *testing.T) {
	resp1 := &http.Response{
		Header:     make(http.Header),
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString(`{"a":"1", "b": 2}`)),
	}
	v1 := &struct {
		A string `json:"a"`
		B int64  `json:"b"`
	}{}
	err := DefaultResponseDecoder(context.TODO(), resp1, v1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.A != "1" {
		t.Errorf("expected %v, got %v", "1", v1.A)
	}
	if v1.B != int64(2) {
		t.Errorf("expected %v, got %v", 2, v1.B)
	}

	resp2 := &http.Response{
		Header:     make(http.Header),
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewBufferString("{badjson}")),
	}
	v2 := &struct {
		A string `json:"a"`
		B int64  `json:"b"`
	}{}
	err = DefaultResponseDecoder(context.TODO(), resp2, v2)
	syntaxErr := &json.SyntaxError{}
	if !errors.As(err, &syntaxErr) {
		t.Errorf("expected %v, got %v", syntaxErr, err)
	}
}

func TestDefaultResponseDecoderHTTPBody(t *testing.T) {
	resp := &http.Response{
		Header:     http.Header{"Content-Type": []string{"application/pdf"}},
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewBufferString("raw response")),
	}
	var body *httpbody.HttpBody
	if err := DefaultResponseDecoder(context.TODO(), resp, &body); err != nil {
		t.Fatal(err)
	}
	if body.GetContentType() != "application/pdf" {
		t.Errorf("expected %v, got %v", "application/pdf", body.GetContentType())
	}
	if string(body.GetData()) != "raw response" {
		t.Errorf("expected %v, got %v", "raw response", string(body.GetData()))
	}
}

func TestDefaultErrorDecoder(t *testing.T) {
	for i := 200; i < 300; i++ {
		resp := &http.Response{Header: make(http.Header), StatusCode: i}
		if DefaultErrorDecoder(context.TODO(), resp) != nil {
			t.Errorf("expected no error, got %v", DefaultErrorDecoder(context.TODO(), resp))
		}
	}
	resp1 := &http.Response{
		Header:     make(http.Header),
		StatusCode: 300,
		Body:       io.NopCloser(bytes.NewBufferString("{\"foo\":\"bar\"}")),
	}
	if DefaultErrorDecoder(context.TODO(), resp1) == nil {
		t.Errorf("expected error, got nil")
	}

	// A body carrying a Forge error is preferred over the status line, because
	// it names the kind and reason the server chose.
	resp2 := &http.Response{
		Header:     http.Header{"Content-Type": []string{ProblemContentType}},
		StatusCode: http.StatusNotFound,
		Body: io.NopCloser(bytes.NewBufferString(
			`{"kind":"NOT_FOUND", "domain":"test.v1", "reason":"FOO", "message":"hi"}`)),
	}
	err := DefaultErrorDecoder(context.TODO(), resp2)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	decoded, ok := err.(*forgeerrors.Error)
	if !ok {
		t.Fatalf("error type = %T, want *forgeerrors.Error", err)
	}
	if decoded.Kind() != forgeerrors.KindNotFound {
		t.Errorf("kind = %v, want KindNotFound", decoded.Kind())
	}
	if decoded.Message() != "hi" {
		t.Errorf("message = %q, want %q", decoded.Message(), "hi")
	}
	if decoded.Reason() != "FOO" {
		t.Errorf("reason = %q, want %q", decoded.Reason(), "FOO")
	}

	// UNKNOWN is a real wire kind, not evidence that the body is unrelated.
	// Its identity and diagnostic fields must survive instead of being replaced
	// by the 500 status fallback.
	respUnknown := &http.Response{
		Header:     http.Header{"Content-Type": []string{ProblemContentType}},
		StatusCode: http.StatusInternalServerError,
		Body: io.NopCloser(bytes.NewBufferString(
			`{"kind":"UNKNOWN", "domain":"test.v1", "reason":"OPAQUE", "message":"redacted"}`)),
	}
	unknown := DefaultErrorDecoder(context.TODO(), respUnknown)
	unknownError, ok := unknown.(*forgeerrors.Error)
	if !ok {
		t.Fatalf("unknown error type = %T, want *forgeerrors.Error", unknown)
	}
	if unknownError.Kind() != forgeerrors.KindUnknown || unknownError.Reason() != "OPAQUE" {
		t.Errorf("unknown error = %v/%q", unknownError.Kind(), unknownError.Reason())
	}

	// A body that carries no Forge error leaves the status line as the only
	// signal, which is what happens when the peer is not a Forge server.
	resp3 := &http.Response{
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		StatusCode: http.StatusServiceUnavailable,
		Body:       io.NopCloser(bytes.NewBufferString(`{"unrelated":"payload"}`)),
	}
	fallback := DefaultErrorDecoder(context.TODO(), resp3)
	if got := forgeerrors.KindOf(fallback); got != forgeerrors.KindUnavailable {
		t.Errorf("fallback kind = %v, want KindUnavailable", got)
	}
}

func TestCodecForResponse(t *testing.T) {
	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Content-Type", "application/xml")
	c := CodecForResponse(resp)
	if !reflect.DeepEqual("xml", c.Name()) {
		t.Errorf("expected %v, got %v", "xml", c.Name())
	}
}

func TestNewClient(t *testing.T) {
	_, err := NewClient(context.Background(), WithEndpoint("127.0.0.1:8888"))
	if err != nil {
		t.Error(err)
	}
	_, err = NewClient(context.Background(), WithEndpoint("127.0.0.1:9999"), WithTLSConfig(&tls.Config{ServerName: "www.kratos.com", RootCAs: nil}))
	if err != nil {
		t.Error(err)
	}
	_, err = NewClient(context.Background(), WithDiscovery(&mockDiscovery{}), WithEndpoint("discovery:///go-kratos"))
	if err != nil {
		t.Error(err)
	}
	_, err = NewClient(context.Background(), WithDiscovery(&mockDiscovery{}), WithEndpoint("127.0.0.1:8888"))
	if err != nil {
		t.Error(err)
	}
	_, err = NewClient(context.Background(), WithEndpoint("127.0.0.1:8888:xxxxa"))
	if err == nil {
		t.Error("except a parseTarget error")
	}
	_, err = NewClient(context.Background(), WithDiscovery(&mockDiscovery{}), WithEndpoint("https://go-kratos.dev/"))
	if err == nil {
		t.Error("err should not be equal to nil")
	}

	client, err := NewClient(
		context.Background(),
		WithDiscovery(&mockDiscovery{}),
		WithEndpoint("discovery:///go-kratos"),
		WithMiddleware(func(handler middleware.UnaryHandler) middleware.UnaryHandler {
			t.Logf("handle in middleware")
			return func(ctx context.Context, req any) (any, error) {
				return handler(ctx, req)
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = client.Invoke(context.Background(), http.MethodPost, "/go", map[string]string{"name": "forge"}, nil, EmptyCallOption{}, &mockCallOption{})
	if err == nil {
		t.Error("err should not be equal to nil")
	}
	err = client.Invoke(context.Background(), http.MethodPost, "/go", map[string]string{"name": "forge"}, nil, EmptyCallOption{}, &mockCallOption{needErr: true})
	if err == nil {
		t.Error("err should be equal to callOption err")
	}
	client.opts.encoder = func(context.Context, string, any) (body []byte, err error) {
		return nil, errors.New("mock test encoder error")
	}
	err = client.Invoke(context.Background(), http.MethodPost, "/go", map[string]string{"name": "forge"}, nil, EmptyCallOption{})
	if err == nil {
		t.Error("err should be equal to encoder error")
	}
}

func TestNewClientWithTLSDoesNotModifyDefaultTransport(t *testing.T) {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Skip("http.DefaultTransport is not *http.Transport")
	}
	originalTLSConfig := defaultTransport.TLSClientConfig

	_, err := NewClient(context.Background(), WithEndpoint("127.0.0.1:9999"), WithTLSConfig(&tls.Config{ServerName: "www.kratos.com"}))
	if err != nil {
		t.Error(err)
	}

	if defaultTransport.TLSClientConfig != originalTLSConfig {
		t.Error("NewClient modified http.DefaultTransport.TLSClientConfig")
	}
}

func TestNewClientAppliesRoundTripperWrappersInOrder(t *testing.T) {
	var events []string
	base := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		events = append(events, "roundtrip:base")
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
		}, nil
	})
	wrapper := func(name string) RoundTripperWrapper {
		return func(next http.RoundTripper) (http.RoundTripper, error) {
			events = append(events, "wrap:"+name)
			return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				events = append(events, "roundtrip:"+name)
				return next.RoundTrip(req)
			}), nil
		}
	}

	client, err := NewClient(
		t.Context(),
		WithEndpoint("http://example.com"),
		WithTransport(base),
		WithRoundTripperWrapper(wrapper("first")),
		WithRoundTripperWrapper(wrapper("second")),
	)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := client.cc.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	want := []string{
		"wrap:second",
		"wrap:first",
		"roundtrip:first",
		"roundtrip:second",
		"roundtrip:base",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestNewClientAppliesRoundTripperWrapperAfterTLSClone(t *testing.T) {
	base := &http.Transport{}
	tlsConfig := &tls.Config{ServerName: "example.com"}
	var wrappedBase http.RoundTripper

	_, err := NewClient(
		t.Context(),
		WithEndpoint("https://example.com"),
		WithTransport(base),
		WithTLSConfig(tlsConfig),
		WithRoundTripperWrapper(func(next http.RoundTripper) (http.RoundTripper, error) {
			wrappedBase = next
			return next, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	cloned, ok := wrappedBase.(*http.Transport)
	if !ok {
		t.Fatalf("wrapped transport type = %T, want *http.Transport", wrappedBase)
	}
	if cloned == base {
		t.Fatal("wrapper received the original transport instead of a clone")
	}
	if cloned.TLSClientConfig != tlsConfig {
		t.Fatal("wrapper did not receive the configured TLS settings")
	}
}

func TestNewClientRejectsInvalidRoundTripperWrappers(t *testing.T) {
	wrapperErr := errors.New("instrument creation failed")
	var typedNil *mockRoundTripper
	tests := []struct {
		name string
		opts []ClientOption
		err  error
	}{
		{
			name: "nil base transport",
			opts: []ClientOption{WithTransport(nil)},
		},
		{
			name: "typed nil base transport",
			opts: []ClientOption{WithTransport(typedNil)},
		},
		{
			name: "nil wrapper",
			opts: []ClientOption{WithRoundTripperWrapper(nil)},
		},
		{
			name: "wrapper error",
			opts: []ClientOption{WithRoundTripperWrapper(func(http.RoundTripper) (http.RoundTripper, error) {
				return nil, wrapperErr
			})},
			err: wrapperErr,
		},
		{
			name: "nil wrapped transport",
			opts: []ClientOption{WithRoundTripperWrapper(func(http.RoundTripper) (http.RoundTripper, error) {
				return nil, nil
			})},
		},
		{
			name: "typed nil wrapped transport",
			opts: []ClientOption{WithRoundTripperWrapper(func(http.RoundTripper) (http.RoundTripper, error) {
				return typedNil, nil
			})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]ClientOption{WithEndpoint("http://example.com")}, tt.opts...)
			_, err := NewClient(t.Context(), opts...)
			if err == nil {
				t.Fatal("NewClient() error = nil")
			}
			if tt.err != nil && !errors.Is(err, tt.err) {
				t.Fatalf("NewClient() error = %v, want wrapped %v", err, tt.err)
			}
		})
	}
}

// The retry layer trusts the transport's claim that a request never left the
// process, so the boundary between "never dialed" and "sent and lost" has to
// hold on real connections rather than by inspection of the code.
func TestClientMarksOnlyUndeliveredRequests(t *testing.T) {
	t.Run("dial failure is marked", func(t *testing.T) {
		// Port 1 on loopback refuses connections, so the request body is
		// never written.
		client, err := NewClient(context.Background(), WithEndpoint("127.0.0.1:1"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })

		var reply struct{}
		err = client.Invoke(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, &reply)
		if err == nil {
			t.Fatal("want a dial failure")
		}
		if !transport.WasNotSent(err) {
			t.Errorf("a refused dial must be marked undelivered, got %v", err)
		}
	})

	t.Run("a delivered request whose reply is lost is not marked", func(t *testing.T) {
		// Read the request to completion, then drop the connection without
		// answering. The server may well have executed the work, so this
		// must not be marked.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				c, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				go func(c net.Conn) {
					defer c.Close()
					buf := make([]byte, 4096)
					_, _ = c.Read(buf)
				}(c)
			}
		}()

		client, err := NewClient(context.Background(), WithEndpoint(ln.Addr().String()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })

		var reply struct{}
		err = client.Invoke(context.Background(), http.MethodPost, "/x", map[string]string{"a": "b"}, &reply)
		if err == nil {
			t.Fatal("want a transport failure")
		}
		if transport.WasNotSent(err) {
			t.Errorf("a request that reached the server must not be marked undelivered, got %v", err)
		}
	})
}
