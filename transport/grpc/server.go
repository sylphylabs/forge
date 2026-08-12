package grpc

import (
	"context"
	"crypto/tls"
	"net"
	"net/url"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/admin"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	"github.com/sylphylabs/forge/internal/endpoint"
	"github.com/sylphylabs/forge/internal/host"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

var (
	_ transport.Server          = (*Server)(nil)
	_ transport.Endpointer      = (*Server)(nil)
	_ transport.Healthzer       = (*Server)(nil)
	_ transport.GracefulStopper = (*Server)(nil)
)

// ServerOption is gRPC server option.
type ServerOption func(o *Server)

// WithNetwork sets the server network.
func WithNetwork(network string) ServerOption {
	return func(s *Server) {
		s.network = network
	}
}

// WithAddress sets the server listen address.
func WithAddress(addr string) ServerOption {
	return func(s *Server) {
		s.address = addr
	}
}

// WithEndpoint sets the endpoint the server advertises to the registry.
func WithEndpoint(endpoint *url.URL) ServerOption {
	return func(s *Server) {
		s.endpoint = endpoint
	}
}

// WithTimeout sets the per-request server timeout.
func WithTimeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.timeout = timeout
	}
}

// WithCustomHealth leaves health service registration to the application.
func WithCustomHealth() ServerOption {
	return func(s *Server) {
		s.customHealth = true
	}
}

// WithTLSConfig sets the server TLS config.
func WithTLSConfig(c *tls.Config) ServerOption {
	return func(s *Server) {
		s.tlsConf = c
	}
}

// WithListener sets the listener the server serves on.
func WithListener(lis net.Listener) ServerOption {
	return func(s *Server) {
		s.lis = lis
	}
}

// WithMiddleware attaches server-wide unary middleware. It is composed once,
// inside NewServer, and runs outside (before) any middleware attached through
// a generated service plan. A nil middleware, or one returning a nil handler,
// is reported by Start and Endpoint, the way a bad listener is.
//
// The client option of the same intent is [WithClientMiddleware].
func WithMiddleware(ms ...middleware.UnaryMiddleware) ServerOption {
	return func(s *Server) {
		s.middleware = append(s.middleware, ms...)
	}
}

// WithStreamMiddleware attaches server-wide stream middleware. It is composed
// once, inside NewServer, and wraps every streaming method outside (before)
// any middleware attached through a generated service plan. At this layer the
// initial request is not yet decoded, so the handler's request argument is
// always nil; per-message behavior comes from decorating the stream.
func WithStreamMiddleware(ms ...middleware.StreamMiddleware) ServerOption {
	return func(s *Server) {
		s.streamMiddleware = append(s.streamMiddleware, ms...)
	}
}

// WithUnaryInterceptor sets the UnaryServerInterceptors for the server.
func WithUnaryInterceptor(in ...grpc.UnaryServerInterceptor) ServerOption {
	return func(s *Server) {
		s.unaryInts = in
	}
}

// WithStreamInterceptor sets the StreamServerInterceptors for the server.
func WithStreamInterceptor(in ...grpc.StreamServerInterceptor) ServerOption {
	return func(s *Server) {
		s.streamInts = in
	}
}

// WithDisableReflection disables gRPC reflection.
func WithDisableReflection() ServerOption {
	return func(s *Server) {
		s.disableReflection = true
	}
}

// WithOptions appends raw grpc.ServerOption values passed through to the
// underlying gRPC server.
func WithOptions(opts ...grpc.ServerOption) ServerOption {
	return func(s *Server) {
		s.grpcOpts = opts
	}
}

// Server is a gRPC server wrapper.
type Server struct {
	*grpc.Server
	baseCtx           context.Context
	tlsConf           *tls.Config
	lis               net.Listener
	err               error
	network           string
	address           string
	endpoint          *url.URL
	timeout           time.Duration
	unaryInts         []grpc.UnaryServerInterceptor
	streamInts        []grpc.StreamServerInterceptor
	middleware        []middleware.UnaryMiddleware
	streamMiddleware  []middleware.StreamMiddleware
	unaryHandler      middleware.UnaryHandler
	streamHandler     middleware.StreamHandler
	grpcOpts          []grpc.ServerOption
	health            *health.Server
	customHealth      bool
	adminClean        func()
	shutdownOnce      sync.Once
	drained           chan struct{}
	disableReflection bool
}

// NewServer creates a gRPC server by options.
func NewServer(opts ...ServerOption) *Server {
	srv := &Server{
		baseCtx: context.Background(),
		network: "tcp",
		address: ":0",
		timeout: 1 * time.Second,
		health:  health.NewServer(),
		drained: make(chan struct{}),
	}
	srv.health.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	for _, o := range opts {
		o(srv)
	}
	srv.composeMiddleware()
	unaryInts := []grpc.UnaryServerInterceptor{
		srv.unaryServerInterceptor(),
	}
	streamInts := []grpc.StreamServerInterceptor{
		srv.streamServerInterceptor(),
	}
	if len(srv.unaryInts) > 0 {
		unaryInts = append(unaryInts, srv.unaryInts...)
	}
	if len(srv.streamInts) > 0 {
		streamInts = append(streamInts, srv.streamInts...)
	}
	grpcOpts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInts...),
		grpc.ChainStreamInterceptor(streamInts...),
	}
	if srv.tlsConf != nil {
		grpcOpts = append(grpcOpts, grpc.Creds(credentials.NewTLS(srv.tlsConf)))
	}
	if len(srv.grpcOpts) > 0 {
		grpcOpts = append(grpcOpts, srv.grpcOpts...)
	}
	srv.Server = grpc.NewServer(grpcOpts...)
	// internal register
	if !srv.customHealth {
		grpc_health_v1.RegisterHealthServer(srv.Server, srv.health)
	}
	// reflection register
	if !srv.disableReflection {
		reflection.Register(srv.Server)
	}
	// admin register
	srv.adminClean, _ = admin.Register(srv.Server)
	return srv
}

// Endpoint return a real address to registry endpoint.
// examples:
//
//	grpc://127.0.0.1:9000?isSecure=false
func (s *Server) Endpoint() (*url.URL, error) {
	if err := s.listenAndEndpoint(); err != nil {
		return nil, s.err
	}
	return s.endpoint, nil
}

// Start start the gRPC server.
func (s *Server) Start(ctx context.Context) error {
	if err := s.listenAndEndpoint(); err != nil {
		return s.err
	}
	s.baseCtx = ctx
	log.Info("[gRPC] server listening", "addr", s.lis.Addr().String())
	s.health.Resume()
	return s.Serve(s.lis)
}

// Healthz reports whether the server accepts new RPCs: true after Start
// resumes serving, false before Start and as soon as a stop begins. It reads
// the lifecycle-driven internal health state; with [WithCustomHealth] the
// registered health service is user-owned and may report differently.
func (s *Server) Healthz() bool {
	resp, err := s.health.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	return err == nil && resp.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING
}

// drain marks the server as not serving and begins the graceful drain of
// in-flight RPCs exactly once. The returned channel closes when the drain
// completes.
func (s *Server) drain() <-chan struct{} {
	s.shutdownOnce.Do(func() {
		if s.adminClean != nil {
			s.adminClean()
		}
		s.health.Shutdown()
		go func() {
			defer close(s.drained)
			log.Info("[gRPC] server stopping")
			s.Server.GracefulStop()
		}()
	})
	return s.drained
}

// GracefulStop stops accepting new RPCs and waits for in-flight RPCs to
// finish. When ctx ends first it returns the context's error and leaves the
// drain running; the caller decides whether to force termination with Stop.
func (s *Server) GracefulStop(ctx context.Context) error {
	select {
	case <-s.drain():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop stop the gRPC server.
func (s *Server) Stop(ctx context.Context) error {
	select {
	case <-s.drain():
	case <-ctx.Done():
		log.Warn("[gRPC] server couldn't stop gracefully in time, doing force stop")
		s.Server.Stop()
	}
	return nil
}

// composeMiddleware composes the server-wide middleware exactly once, during
// construction. The composed handlers close over a pointer cell each
// interceptor fills per call, so composition cost is paid here and never on
// the request path. A compose failure is stored in s.err and surfaces from
// Start and Endpoint, where a bad listener surfaces too.
func (s *Server) composeMiddleware() {
	unary, err := middleware.ComposeUnary(func(ctx context.Context, req any) (any, error) {
		c := callFrom(ctx)
		if c.unary == nil {
			return nil, errLostCall
		}
		return c.unary(ctx, req)
	}, s.middleware...)
	if err != nil {
		s.err = err
		return
	}
	s.unaryHandler = unary
	stream, err := middleware.ComposeStream(func(request any, ss middleware.ServerStream) error {
		c := callFrom(ss.Context())
		if c.stream == nil {
			return errLostCall
		}
		return c.stream(request, ss)
	}, s.streamMiddleware...)
	if err != nil {
		s.err = err
		return
	}
	s.streamHandler = stream
}

func (s *Server) listenAndEndpoint() error {
	if s.err != nil {
		return s.err
	}
	if s.lis == nil {
		lis, err := net.Listen(s.network, s.address)
		if err != nil {
			s.err = err
			return err
		}
		s.lis = lis
	}
	if s.endpoint == nil {
		addr, err := host.Extract(s.address, s.lis)
		if err != nil {
			s.err = err
			return err
		}
		s.endpoint = endpoint.NewEndpoint(endpoint.Scheme("grpc", s.tlsConf != nil), addr)
	}
	return s.err
}
