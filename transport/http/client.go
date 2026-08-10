package http

import (
	"bytes"
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"time"

	"github.com/sylphylabs/forge/encoding"
	"github.com/sylphylabs/forge/internal/host"
	"github.com/sylphylabs/forge/internal/httputil"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/wrr"
	"github.com/sylphylabs/forge/transport"
)

// DecodeErrorFunc is decode error func.
type DecodeErrorFunc func(ctx context.Context, res *http.Response) error

// EncodeRequestFunc is request encode func.
type EncodeRequestFunc func(ctx context.Context, contentType string, in any) (body []byte, err error)

// DecodeResponseFunc is response decode func.
type DecodeResponseFunc func(ctx context.Context, res *http.Response, out any) error

// ClientOption is HTTP client option.
type ClientOption func(*clientOptions)

// RoundTripperWrapper decorates an HTTP client transport.
type RoundTripperWrapper func(http.RoundTripper) (http.RoundTripper, error)

// Client is an HTTP transport client.
type clientOptions struct {
	ctx                  context.Context
	tlsConf              *tls.Config
	timeout              time.Duration
	endpoint             string
	userAgent            string
	encoder              EncodeRequestFunc
	decoder              DecodeResponseFunc
	errorDecoder         DecodeErrorFunc
	transport            http.RoundTripper
	roundTripperWrappers []RoundTripperWrapper
	nodeFilters          []selector.NodeFilter
	selectorBuilder      selector.Builder
	discovery            registry.Discovery
	middleware           []middleware.UnaryMiddleware
	block                bool
	subsetSize           int
}

// WithSelector sets the load-balancing policy this client uses to pick a node
// among the ones discovery reports. Each client builds its own selector from
// the builder, so two clients in one process can balance differently.
func WithSelector(builder selector.Builder) ClientOption {
	return func(o *clientOptions) {
		o.selectorBuilder = builder
	}
}

// WithSubset with client discovery subset size.
// zero value means subset filter disabled
func WithSubset(size int) ClientOption {
	return func(o *clientOptions) {
		o.subsetSize = size
	}
}

// WithTransport with client transport.
func WithTransport(trans http.RoundTripper) ClientOption {
	return func(o *clientOptions) {
		o.transport = trans
	}
}

// WithRoundTripperWrapper appends transport decorators. The first wrapper is
// the outermost wrapper and observes each request first.
func WithRoundTripperWrapper(wrappers ...RoundTripperWrapper) ClientOption {
	return func(o *clientOptions) {
		o.roundTripperWrappers = append(o.roundTripperWrappers, wrappers...)
	}
}

// WithTimeout with client request timeout.
func WithTimeout(d time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = d
	}
}

// WithUserAgent with client user agent.
func WithUserAgent(ua string) ClientOption {
	return func(o *clientOptions) {
		o.userAgent = ua
	}
}

// WithMiddleware with client middleware.
func WithMiddleware(m ...middleware.UnaryMiddleware) ClientOption {
	return func(o *clientOptions) {
		o.middleware = m
	}
}

// WithEndpoint with client addr.
func WithEndpoint(endpoint string) ClientOption {
	return func(o *clientOptions) {
		o.endpoint = endpoint
	}
}

// WithRequestEncoder with client request encoder.
func WithRequestEncoder(encoder EncodeRequestFunc) ClientOption {
	return func(o *clientOptions) {
		o.encoder = encoder
	}
}

// WithResponseDecoder with client response decoder.
func WithResponseDecoder(decoder DecodeResponseFunc) ClientOption {
	return func(o *clientOptions) {
		o.decoder = decoder
	}
}

// WithErrorDecoder with client error decoder.
func WithErrorDecoder(errorDecoder DecodeErrorFunc) ClientOption {
	return func(o *clientOptions) {
		o.errorDecoder = errorDecoder
	}
}

// WithDiscovery with client discovery.
func WithDiscovery(d registry.Discovery) ClientOption {
	return func(o *clientOptions) {
		o.discovery = d
	}
}

// WithNodeFilter with select filters
func WithNodeFilter(filters ...selector.NodeFilter) ClientOption {
	return func(o *clientOptions) {
		o.nodeFilters = filters
	}
}

// WithBlock with client block.
func WithBlock() ClientOption {
	return func(o *clientOptions) {
		o.block = true
	}
}

// WithTLSConfig with tls config.
func WithTLSConfig(c *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.tlsConf = c
	}
}

// Client is an HTTP client.
type Client struct {
	opts     clientOptions
	target   *Target
	r        *resolver
	cc       *http.Client
	insecure bool
	selector selector.Selector
}

// NewClient returns an HTTP client.
func NewClient(ctx context.Context, opts ...ClientOption) (*Client, error) {
	options := clientOptions{
		ctx:          ctx,
		timeout:      2000 * time.Millisecond,
		encoder:      DefaultRequestEncoder,
		decoder:      DefaultResponseDecoder,
		errorDecoder: DefaultErrorDecoder,
		transport:    http.DefaultTransport,
		subsetSize:   25,
		// Weighted round robin is the default policy because it needs no
		// feedback beyond the weights discovery already reports, so it behaves
		// sensibly for a client that never states a preference.
		selectorBuilder: wrr.NewBuilder(),
	}
	for _, o := range opts {
		o(&options)
	}
	if options.selectorBuilder == nil {
		return nil, stderrors.New("[http client] selector builder is nil")
	}
	if isNilRoundTripper(options.transport) {
		return nil, stderrors.New("[http client] transport is nil")
	}
	for i, wrapper := range options.roundTripperWrappers {
		if wrapper == nil {
			return nil, fmt.Errorf("[http client] round tripper wrapper %d is nil", i)
		}
	}
	if options.tlsConf != nil {
		if tr, ok := options.transport.(*http.Transport); ok {
			cloned := tr.Clone()
			cloned.TLSClientConfig = options.tlsConf
			options.transport = cloned
		}
	}
	for i := len(options.roundTripperWrappers) - 1; i >= 0; i-- {
		wrapped, err := options.roundTripperWrappers[i](options.transport)
		if err != nil {
			return nil, fmt.Errorf("[http client] round tripper wrapper %d failed: %w", i, err)
		}
		if isNilRoundTripper(wrapped) {
			return nil, fmt.Errorf("[http client] round tripper wrapper %d returned nil", i)
		}
		options.transport = wrapped
	}
	insecure := options.tlsConf == nil
	target, err := parseTarget(options.endpoint, insecure)
	if err != nil {
		return nil, err
	}
	selector := options.selectorBuilder.Build()
	var r *resolver
	if options.discovery != nil {
		if target.Scheme == schemeDiscovery {
			if r, err = newResolver(ctx, options.discovery, target, selector, options.block, insecure, options.subsetSize); err != nil {
				return nil, fmt.Errorf("[http client] new resolver failed for endpoint %q: %w", options.endpoint, err)
			}
		} else if _, _, err := host.ExtractHostPort(options.endpoint); err != nil {
			return nil, fmt.Errorf("[http client] invalid endpoint format %q: %w", options.endpoint, err)
		}
	}
	return &Client{
		opts:     options,
		target:   target,
		insecure: insecure,
		r:        r,
		cc: &http.Client{
			Timeout:   options.timeout,
			Transport: options.transport,
		},
		selector: selector,
	}, nil
}

func isNilRoundTripper(roundTripper http.RoundTripper) bool {
	if roundTripper == nil {
		return true
	}
	value := reflect.ValueOf(roundTripper)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// Invoke makes a rpc call procedure for remote service.
func (client *Client) Invoke(ctx context.Context, method, path string, args any, reply any, opts ...CallOption) error {
	var (
		contentType string
		body        io.Reader
	)
	c := defaultCallInfo(path)
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return err
		}
	}
	if args != nil {
		data, err := client.opts.encoder(ctx, c.contentType, args)
		if err != nil {
			return err
		}
		contentType = c.contentType
		body = bytes.NewReader(data)
	}
	requestURL, err := client.target.requestURL(path)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(method, requestURL, body)
	if err != nil {
		return err
	}
	if c.headerCarrier != nil {
		req.Header = *c.headerCarrier
	}

	if contentType != "" {
		req.Header.Set("Content-Type", c.contentType)
	}
	if c.accept != "" {
		req.Header.Set("Accept", c.accept)
	}
	if client.opts.userAgent != "" {
		req.Header.Set("User-Agent", client.opts.userAgent)
	}
	ctx = transport.NewClientContext(ctx, &Transport{
		endpoint:     client.opts.endpoint,
		reqHeader:    headerCarrier(req.Header),
		operation:    c.operation,
		request:      req,
		pathTemplate: c.pathTemplate,
	})
	return client.invoke(ctx, req, args, reply, c, opts...)
}

func (client *Client) invoke(ctx context.Context, req *http.Request, args any, reply any, c callInfo, opts ...CallOption) error {
	h := func(ctx context.Context, _ any) (any, error) {
		res, err := client.do(req.WithContext(ctx))
		if res != nil {
			cs := csAttempt{res: res}
			for _, o := range opts {
				o.after(&c, &cs)
			}
		}
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if err := client.opts.decoder(ctx, res, reply); err != nil {
			return nil, err
		}
		return reply, nil
	}
	var p selector.Peer
	ctx = selector.NewPeerContext(ctx, &p)
	if len(client.opts.middleware) > 0 {
		h = middleware.ChainUnary(client.opts.middleware...)(h)
	}
	_, err := h(ctx, args)
	return err
}

// Do send an HTTP request and decodes the body of response into target.
// returns an error (of type *Error) if the response status code is not 2xx.
func (client *Client) Do(req *http.Request, opts ...CallOption) (*http.Response, error) {
	c := defaultCallInfo(req.URL.Path)
	for _, o := range opts {
		if err := o.before(&c); err != nil {
			return nil, err
		}
	}

	return client.do(req)
}

func (client *Client) do(req *http.Request) (*http.Response, error) {
	var done func(context.Context, selector.DoneInfo)
	if client.r != nil {
		if _, ok := transport.FromClientContext(req.Context()); !ok {
			req = req.WithContext(transport.NewClientContext(req.Context(), &Transport{
				endpoint:  client.opts.endpoint,
				reqHeader: headerCarrier(req.Header),
				request:   req,
			}))
		}
		var (
			err  error
			node selector.Node
		)
		if node, done, err = client.selector.Select(req.Context(), selector.WithNodeFilter(client.opts.nodeFilters...)); err != nil {
			// Selection failed, so no connection was ever attempted and no
			// byte of the request left this process.
			return nil, transport.MarkNotSent(ErrNodeNotFound.Msg(err.Error()).Wrap(err))
		}
		if client.insecure {
			req.URL.Scheme = schemeHTTP
		} else {
			req.URL.Scheme = schemeHTTPS
		}
		req.URL.Host = node.Address()
		req.Host = node.Address()
	}
	resp, err := client.cc.Do(req)
	if err != nil {
		err = markUndelivered(err)
	}
	if err == nil {
		t, ok := transport.FromClientContext(req.Context())
		if ok {
			ht, ok := t.(*Transport)
			if ok {
				ht.replyHeader = headerCarrier(resp.Header)
			}
		}
		err = client.opts.errorDecoder(req.Context(), resp)
	}
	if done != nil {
		done(req.Context(), selector.DoneInfo{Err: err})
	}
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// markUndelivered attaches [transport.MarkNotSent] to the errors an HTTP
// round trip produces only before the request reaches the wire, and leaves
// every other error untouched.
//
// The one shape that qualifies is a *net.OpError with Op "dial" anywhere in
// the chain. net/http's Transport writes a request only after a connection is
// established, so a failure attributed to the dial — connection refused, host
// unresolved, TLS handshake rejected, the dial deadline expiring — happened
// with no connection to write to. Acquiring a pooled connection funnels into
// the same dial when the pool is empty, and a failure to obtain one surfaces
// as that dial's error.
//
// Everything else stays unmarked, including the shapes that look retryable.
// A request that was written and then lost its connection surfaces as a bare
// io.EOF under *url.Error with no *net.OpError, which is indistinguishable at
// this layer from a server that executed the request and failed to reply. A
// response-side read error carries an OpError with Op "read", by which point
// the request has certainly been delivered. Both are left for the caller to
// treat as "may have executed".
func markUndelivered(err error) error {
	var oe *net.OpError
	if stderrors.As(err, &oe) && oe.Op == "dial" {
		return transport.MarkNotSent(err)
	}
	return err
}

// Close tears down the Transport and all underlying connections.
func (client *Client) Close() error {
	if client.r != nil {
		return client.r.Close()
	}
	return nil
}

// DefaultRequestEncoder is an HTTP request encoder.
func DefaultRequestEncoder(_ context.Context, contentType string, in any) ([]byte, error) {
	if body, ok := httpBody(in); ok {
		return body.GetData(), nil
	}
	name := httputil.ContentSubtype(contentType)
	codec := encoding.GetCodec(name)
	if codec == nil {
		return nil, ErrCodec.Msgf("unregistered Content-Type: %s", contentType)
	}
	body, err := encodeWithCodec(codec, in)
	if err != nil {
		return nil, err
	}
	return body, err
}

// DefaultResponseDecoder is an HTTP response decoder.
func DefaultResponseDecoder(_ context.Context, res *http.Response, v any) error {
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if body, ok := httpBody(v); ok {
		body.SetContentType(res.Header.Get("Content-Type"))
		body.SetData(data)
		return nil
	}
	return decodeWithCodec(CodecForResponse(res), data, v)
}

// DefaultErrorDecoder is an HTTP error decoder.
//
// A body carrying a Forge error representation is preferred, because it names
// the kind and reason the server chose. The status line is only a fallback: it
// is the one signal available when the peer is not a Forge server, or when a
// proxy replaced the body.
func DefaultErrorDecoder(_ context.Context, res *http.Response) error {
	if res.StatusCode >= 200 && res.StatusCode <= 299 {
		return nil
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err == nil {
		if decoded, ok := unmarshalProblem(res.Header.Get("Content-Type"), data, res.StatusCode); ok {
			return decoded
		}
	}
	return ErrorFromStatus(res.StatusCode).Wrap(err)
}

// CodecForResponse get encoding.Codec via http.Response
func CodecForResponse(r *http.Response) encoding.Codec {
	codec := encoding.GetCodec(httputil.ContentSubtype(r.Header.Get("Content-Type")))
	if codec != nil {
		return codec
	}
	return encoding.GetCodec("json")
}
