package grpc

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	grpcinsecure "google.golang.org/grpc/credentials/insecure"
	grpcmd "google.golang.org/grpc/metadata"

	"github.com/openkratos/kratos/internal/matcher"
	"github.com/openkratos/kratos/middleware"
	"github.com/openkratos/kratos/registry"
	"github.com/openkratos/kratos/selector"
	"github.com/openkratos/kratos/selector/wrr"
	"github.com/openkratos/kratos/transport"
	"github.com/openkratos/kratos/transport/grpc/resolver/discovery"

	// init resolver
	_ "github.com/openkratos/kratos/transport/grpc/resolver/direct"
)

func init() {
	if selector.GlobalSelector() == nil {
		selector.SetGlobalSelector(wrr.NewBuilder())
	}
}

// ClientOption is gRPC client option.
type ClientOption func(o *clientOptions)

// WithEndpoint with client endpoint.
func WithEndpoint(endpoint string) ClientOption {
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

// WithTimeout with client timeout.
func WithTimeout(timeout time.Duration) ClientOption {
	return func(o *clientOptions) {
		o.timeout = timeout
	}
}

// WithMiddleware with client middleware.
func WithMiddleware(m ...middleware.UnaryMiddleware) ClientOption {
	return func(o *clientOptions) {
		o.middleware = m
	}
}

// WithStreamMiddleware with client stream middleware.
func WithStreamMiddleware(m ...middleware.UnaryMiddleware) ClientOption {
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

// WithTLSConfig with TLS config.
func WithTLSConfig(c *tls.Config) ClientOption {
	return func(o *clientOptions) {
		o.tlsConf = c
	}
}

// WithUnaryInterceptor returns a ClientOption that specifies the interceptor for unary RPCs.
func WithUnaryInterceptor(in ...grpc.UnaryClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.ints = in
	}
}

// WithStreamInterceptor returns a ClientOption that specifies the interceptor for streaming RPCs.
func WithStreamInterceptor(in ...grpc.StreamClientInterceptor) ClientOption {
	return func(o *clientOptions) {
		o.streamInts = in
	}
}

// WithOptions with gRPC options.
func WithOptions(opts ...grpc.DialOption) ClientOption {
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
		grpcOpts = append(grpcOpts,
			grpc.WithResolvers(
				discovery.NewBuilder(
					options.discovery,
					discovery.WithInsecure(isInsecure),
					discovery.WithTimeout(options.timeout),
					discovery.WithSubset(options.subsetSize),
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
					keyvals = append(keyvals, k, header.Get(k))
				}
				ctx = grpcmd.AppendToOutgoingContext(ctx, keyvals...)
			}
			return reply, invoker(ctx, method, req, reply, cc, opts...)
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

// wrappedClientStream wraps the grpc.ClientStream and applies middleware
type wrappedClientStream struct {
	grpc.ClientStream
	ctx        context.Context
	middleware matcher.Matcher
}

func (w *wrappedClientStream) Context() context.Context {
	return w.ctx
}

func (w *wrappedClientStream) SendMsg(m any) error {
	h := func(_ context.Context, req any) (any, error) {
		return req, w.ClientStream.SendMsg(m)
	}

	info, ok := transport.FromClientContext(w.ctx)
	if !ok {
		return fmt.Errorf("transport value stored in ctx returns: %v", ok)
	}

	if next := w.middleware.Match(info.Operation()); len(next) > 0 {
		h = middleware.ChainUnary(next...)(h)
	}

	_, err := h(w.ctx, m)
	return err
}

func (w *wrappedClientStream) RecvMsg(m any) error {
	h := func(_ context.Context, req any) (any, error) {
		return req, w.ClientStream.RecvMsg(m)
	}

	info, ok := transport.FromClientContext(w.ctx)
	if !ok {
		return fmt.Errorf("transport value stored in ctx returns: %v", ok)
	}

	if next := w.middleware.Match(info.Operation()); len(next) > 0 {
		h = middleware.ChainUnary(next...)(h)
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
			return nil, err
		}

		h := func(_ context.Context, _ any) (any, error) {
			return streamer, nil
		}

		m := matcher.New()
		if len(ms) > 0 {
			m.Use(ms...)
			middleware.ChainUnary(ms...)(h)
		}

		wrappedStream := &wrappedClientStream{
			ClientStream: clientStream,
			ctx:          ctx,
			middleware:   m,
		}

		return wrappedStream, nil
	}
}
