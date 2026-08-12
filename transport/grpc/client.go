package grpc

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	grpcmd "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/transport"
	"github.com/sylphylabs/forge/transport/grpc/resolver/discovery"

	// init resolver
	_ "github.com/sylphylabs/forge/transport/grpc/resolver/direct"
)

// ClientOption is gRPC client option.
type ClientOption func(o *clientOptions)

// WithTarget sets the target the client dials: a host:port, or a
// discovery:/// service name resolved through the configured discovery.
func WithTarget(endpoint string) ClientOption {
	return func(o *clientOptions) {
		o.endpoint = endpoint
	}
}

// WithSubset with client discovery subset size.
// zero value means subset filter disabled
func WithSubset(size int) ClientOption {
	return func(o *clientOptions) {
		o.subsetSize = size
	}
}

// WithRequestTimeout bounds each unary RPC with a per-call deadline.
func WithRequestTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = timeout
	}
}

// WithClientMiddleware attaches client-side unary middleware around each call.
func WithClientMiddleware(m ...middleware.UnaryMiddleware) ClientOption {
	return func(o *clientOptions) {
		o.middleware = m
	}
}

// WithClientStreamMiddleware attaches client-side middleware around each
// message send and receive on a stream.
func WithClientStreamMiddleware(m ...middleware.UnaryMiddleware) ClientOption {
	return func(o *clientOptions) {
		o.streamMiddleware = m
	}
}

// WithDiscovery with client discovery.
func WithDiscovery(d registry.Discovery) ClientOption {
	return func(o *clientOptions) {
		o.discovery = d
	}
}

// WithClientTLSConfig sets the TLS config the client dials with.
func WithClientTLSConfig(c *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.tlsConf = c
	}
}

// WithUnaryClientInterceptor specifies the interceptors for unary RPCs.
func WithUnaryClientInterceptor(in ...grpc.UnaryClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.ints = in
	}
}

// WithStreamClientInterceptor specifies the interceptors for streaming RPCs.
func WithStreamClientInterceptor(in ...grpc.StreamClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.streamInts = in
	}
}

// WithDialOptions appends raw grpc.DialOption values passed through to the
// underlying connection.
func WithDialOptions(opts ...grpc.DialOption) ClientOption {
	return func(o *clientOptions) {
		o.grpcOpts = opts
	}
}

// WithNodeFilter with select filters
func WithNodeFilter(filters ...selector.NodeFilter) ClientOption {
	return func(o *clientOptions) {
		o.filters = filters
	}
}

// WithSelector sets the load-balancing policy this client uses to pick a node
// among the ones discovery reports. Each client balances independently.
//
// The policy applies to a discovery endpoint. A client dialing a fixed address
// has one node to choose from, so no policy is consulted.
func WithSelector(sb selector.Builder) ClientOption {
	return func(o *clientOptions) {
		o.selectorBuilder = sb
	}
}

// WithHealthCheck with health check
func WithHealthCheck(healthCheck bool) ClientOption {
	return func(o *clientOptions) {
		if !healthCheck {
			o.healthCheckConfig = ""
		}
	}
}

// clientOptions is gRPC Client
type clientOptions struct {
	endpoint          string
	subsetSize        int
	tlsConf           *tls.Config
	timeout           time.Duration
	discovery         registry.Discovery
	middleware        []middleware.UnaryMiddleware
	streamMiddleware  []middleware.UnaryMiddleware
	ints              []grpc.UnaryClientInterceptor
	streamInts        []grpc.StreamClientInterceptor
	grpcOpts          []grpc.DialOption
	balancerName      string
	filters           []selector.NodeFilter
	selectorBuilder   selector.Builder
	healthCheckConfig string
}

// NewClient returns a gRPC client connection.
func NewClient(ctx context.Context, opts ...ClientOption) (*grpc.ClientConn, error) {
	options := clientOptions{
		timeout:           2000 * time.Millisecond,
		balancerName:      balancerName,
		subsetSize:        25,
		healthCheckConfig: `,"healthCheckConfig":{"serviceName":""}`,
	}
	for _, o := range opts {
		o(&options)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	isInsecure := options.tlsConf == nil
	ints := []grpc.UnaryClientInterceptor{
		unaryClientInterceptor(options.middleware, options.timeout, options.filters),
	}
	sints := []grpc.StreamClientInterceptor{
		streamClientInterceptor(options.streamMiddleware, options.filters),
	}

	if len(options.ints) > 0 {
		ints = append(ints, options.ints...)
	}
	if len(options.streamInts) > 0 {
		sints = append(sints, options.streamInts...)
	}
	grpcOpts := []grpc.DialOption{
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"loadBalancingConfig": [{"%s":{}}]%s}`,
			options.balancerName, options.healthCheckConfig)),
		grpc.WithChainUnaryInterceptor(ints...),
		grpc.WithChainStreamInterceptor(sints...),
	}

	if options.discovery != nil {
		// The resolver's watcher-creation timeout is deliberately not wired to
		// options.timeout: that value bounds a single RPC, while establishing a
		// registry watch is a one-time setup cost with its own default
		// (discovery.DefaultWatchTimeout), tunable via discovery.WithTimeout.
		grpcOpts = append(grpcOpts,
			grpc.WithResolvers(
				discovery.NewBuilder(
					options.discovery,
					discovery.WithInsecure(isInsecure),
					discovery.WithSubset(options.subsetSize),
					discovery.WithSelector(options.selectorBuilder),
				)))
	}
	if isInsecure {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(grpcinsecure.NewCredentials()))
	} else {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(credentials.NewTLS(options.tlsConf)))
	}
	if len(options.grpcOpts) > 0 {
		grpcOpts = append(grpcOpts, options.grpcOpts...)
	}
	conn, err := grpc.NewClient(options.endpoint, grpcOpts...)
	if err != nil {
		return nil, err
	}
	conn.Connect()
	return conn, nil
}

func unaryClientInterceptor(ms []middleware.UnaryMiddleware, timeout time.Duration, filters []selector.NodeFilter) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = transport.NewClientContext(ctx, &Transport{
			endpoint:    cc.Target(),
			operation:   method,
			reqHeader:   headerCarrier{},
			nodeFilters: filters,
		})
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		h := func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromClientContext(ctx); ok {
				header := tr.RequestHeader()
				keys := header.Keys()
				keyvals := make([]string, 0, len(keys))
				for _, k := range keys {
					for _, v := range header.Values(k) {
						keyvals = append(keyvals, k, v)
					}
				}
				ctx = grpcmd.AppendToOutgoingContext(ctx, keyvals...)
			}
			// Convert here, inside the invoker, so middleware sees a Forge
			// error rather than a raw status. Retry and circuit breaking
			// classify by Kind, and the errors package cannot recognize a
			// status on its own.
			return reply, convertClientError(invoker(ctx, method, req, reply, cc, opts...))
		}
		if len(ms) > 0 {
			h = middleware.ChainUnary(ms...)(h)
		}
		var p selector.Peer
		ctx = selector.NewPeerContext(ctx, &p)
		_, err := h(ctx, req)
		return err
	}
}

// convertClientError makes a server's gRPC status reachable as a Forge error,
// so a caller matches a remote failure against the same sentinel it would use
// for a local one.
//
// The conversion happens here because the errors package holds no gRPC types:
// the transport that understands the status is the one that translates it.
//
// The original status is preserved rather than replaced. Callers and
// interceptors that read a code with status.Code or status.FromError must keep
// working, so the result carries both views: errors.As reaches the Forge error,
// and GRPCStatus still reports the status the server sent.
//
// No error is marked with [transport.MarkNotSent], which withholds automatic
// retries from non-idempotent gRPC calls by design. grpc-go supplies no
// evidence this layer can act on: a call that never found a listener and a
// call whose connection died after the request was written both arrive as a
// codes.Unavailable *status.Error with no Unwrap method, no typed cause and no
// status detail. The two are separated only by free text inside the message
// ("Error while dialing" versus "error reading server preface"), which is an
// unstable internal string and, more importantly, still cannot rule out the
// case that actually matters — a request delivered and executed whose reply
// was lost. Marking on that basis would assert proof this transport does not
// have. Callers whose gRPC operations tolerate repeat execution declare it
// with retry.Idempotent.
func convertClientError(err error) error {
	if err == nil {
		return nil
	}
	var already *errors.Error
	if stderrors.As(err, &already) {
		return err
	}
	converted, ok := ErrorFrom(err)
	if !ok {
		return err
	}
	gs, _ := status.FromError(err)
	return &statusError{err: converted, status: gs}
}

// wrappedClientStream wraps the grpc.ClientStream and applies middleware
type wrappedClientStream struct {
	grpc.ClientStream
	ctx        context.Context
	middleware []middleware.UnaryMiddleware
}

func (w *wrappedClientStream) Context() context.Context {
	return w.ctx
}

func (w *wrappedClientStream) SendMsg(m any) error {
	// Convert inside the handler, as the unary path does, so middleware sees a
	// Forge error rather than a raw status: retry and circuit breaking classify
	// by Kind, and the errors package cannot recognize a status on its own.
	h := func(_ context.Context, req any) (any, error) {
		return req, convertClientError(w.ClientStream.SendMsg(m))
	}

	_, ok := transport.FromClientContext(w.ctx)
	if !ok {
		return fmt.Errorf("transport value stored in ctx returns: %v", ok)
	}

	if len(w.middleware) > 0 {
		h = middleware.ChainUnary(w.middleware...)(h)
	}

	_, err := h(w.ctx, m)
	return err
}

func (w *wrappedClientStream) RecvMsg(m any) error {
	h := func(_ context.Context, req any) (any, error) {
		return req, convertClientError(w.ClientStream.RecvMsg(m))
	}

	_, ok := transport.FromClientContext(w.ctx)
	if !ok {
		return fmt.Errorf("transport value stored in ctx returns: %v", ok)
	}

	if len(w.middleware) > 0 {
		h = middleware.ChainUnary(w.middleware...)(h)
	}

	_, err := h(w.ctx, m)
	return err
}

func streamClientInterceptor(ms []middleware.UnaryMiddleware, filters []selector.NodeFilter) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) { // nolint
		ctx = transport.NewClientContext(ctx, &Transport{
			endpoint:    cc.Target(),
			operation:   method,
			reqHeader:   headerCarrier{},
			nodeFilters: filters,
		})
		var p selector.Peer
		ctx = selector.NewPeerContext(ctx, &p)

		clientStream, err := streamer(ctx, desc, cc, method, opts...)
		if err != nil {
			return nil, convertClientError(err)
		}

		wrappedStream := &wrappedClientStream{
			ClientStream: clientStream,
			ctx:          ctx,
			middleware:   append([]middleware.UnaryMiddleware(nil), ms...),
		}

		return wrappedStream, nil
	}
}
