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

// Network with server network.
func Network(network string) ServerOption {
	return func(s *Server) {
		s.network = network
	}
}

// Address with server address.
func Address(addr string) ServerOption {
	return func(s *Server) {
		s.address = addr
	}
}

// Endpoint with server address.
func Endpoint(endpoint *url.URL) ServerOption {
	return func(s *Server) {
		s.endpoint = endpoint
	}
}

// Timeout with server timeout.
func Timeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.timeout = timeout
	}
}

// CustomHealth Checks server.
func CustomHealth() ServerOption {
	return func(s *Server) {
		s.customHealth = true
	}
}

// TLSConfig with TLS config.
func TLSConfig(c *tls.Config) ServerOption {
	return func(s *Server) {
		s.tlsConf = c
	}
}

// Listener with server lis
func Listener(lis net.Listener) ServerOption {
	return func(s *Server) {
		s.lis = lis
	}
}

// UnaryInterceptor returns a ServerOption that sets the UnaryServerInterceptor for the server.
func UnaryInterceptor(in ...grpc.UnaryServerInterceptor) ServerOption {
	return func(s *Server) {
		s.unaryInts = in
	}
}

// StreamInterceptor returns a ServerOption that sets the StreamServerInterceptor for the server.
func StreamInterceptor(in ...grpc.StreamServerInterceptor) ServerOption {
	return func(s *Server) {
		s.streamInts = in
	}
}

// DisableReflection disable grpc reflection.
func DisableReflection() ServerOption {
	return func(s *Server) {
		s.disableReflection = true
	}
}

// Options with grpc options.
func Options(opts ...grpc.ServerOption) ServerOption {
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
// the lifecycle-driven internal health state; with [CustomHealth] the
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

func (s *Server) listenAndEndpoint() error {
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
