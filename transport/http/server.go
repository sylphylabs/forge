package http

import (
	"context"
	"crypto/tls"
	stderrors "errors"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

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
	_ http.Handler              = (*Server)(nil)
)

// ServerOption is an HTTP server option.
type ServerOption func(*Server)

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

// Filter with HTTP middleware option.
func Filter(filters ...FilterFunc) ServerOption {
	return func(o *Server) {
		o.filters = filters
	}
}

// RequestVarsDecoder with request decoder.
func RequestVarsDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decVars = dec
	}
}

// RequestQueryDecoder with request decoder.
func RequestQueryDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decQuery = dec
	}
}

// RequestDecoder with request decoder.
func RequestDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decBody = dec
	}
}

// ResponseEncoder with response encoder.
func ResponseEncoder(en EncodeResponseFunc) ServerOption {
	return func(o *Server) {
		o.enc = en
	}
}

// ErrorEncoder with error encoder.
func ErrorEncoder(en EncodeErrorFunc) ServerOption {
	return func(o *Server) {
		o.ene = en
	}
}

// TLSConfig with TLS config.
func TLSConfig(c *tls.Config) ServerOption {
	return func(o *Server) {
		o.tlsConf = c
	}
}

// Listener with server lis
func Listener(lis net.Listener) ServerOption {
	return func(s *Server) {
		s.lis = lis
	}
}

// PathPrefix applies a common prefix to routes registered on the server.
func PathPrefix(prefix string) ServerOption {
	return func(s *Server) {
		s.pathPrefix = prefix
	}
}

func NotFoundHandler(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.router.setNotFoundHandler(handler)
	}
}

func MethodNotAllowedHandler(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.router.setMethodNotAllowedHandler(handler)
	}
}

// Server is an HTTP server wrapper.
type Server struct {
	*http.Server
	lis        net.Listener
	tlsConf    *tls.Config
	endpoint   *url.URL
	err        error
	network    string
	address    string
	timeout    time.Duration
	filters    []FilterFunc
	decVars    DecodeRequestFunc
	decQuery   DecodeRequestFunc
	decBody    DecodeRequestFunc
	enc        EncodeResponseFunc
	ene        EncodeErrorFunc
	pathPrefix string
	router     *routeMux
	serving    atomic.Bool
}

// NewServer creates an HTTP server by options.
func NewServer(opts ...ServerOption) *Server {
	srv := &Server{
		network:  "tcp",
		address:  ":0",
		timeout:  1 * time.Second,
		decVars:  DefaultRequestVars,
		decQuery: DefaultRequestQuery,
		decBody:  DefaultRequestDecoder,
		enc:      DefaultResponseEncoder,
		ene:      DefaultErrorEncoder,
		router:   newRouteMux(),
	}
	for _, o := range opts {
		o(srv)
	}
	srv.router.setErrorEncoder(srv.ene)
	srv.Server = &http.Server{
		Handler:   FilterChain(srv.filters...)(srv.router),
		TLSConfig: srv.tlsConf,
	}
	return srv
}

// WalkRoute walks the router and all its sub-routers, calling walkFn for each route in the tree.
func (s *Server) WalkRoute(fn WalkRouteFunc) error {
	return s.router.walk(fn)
}

// WalkHandle walks the router and all its sub-routers, calling walkFn for each route in the tree.
func (s *Server) WalkHandle(handle func(method, path string, handler http.HandlerFunc)) error {
	return s.WalkRoute(func(r RouteInfo) error {
		handle(r.Method, r.Path, s.ServeHTTP)
		return nil
	})
}

// Route registers an HTTP router.
func (s *Server) Route(prefix string, filters ...FilterFunc) *Router {
	return newRouter(joinRoutePath(s.pathPrefix, prefix), s, filters...)
}

// Handle registers a new route with a matcher for the URL path.
func (s *Server) Handle(path string, h http.Handler) {
	s.router.handle("*", joinRoutePath(s.pathPrefix, path), s.filter()(h), false)
}

// HandlePrefix registers a new route with a matcher for the URL path prefix.
func (s *Server) HandlePrefix(prefix string, h http.Handler) {
	s.router.handlePrefix(joinRoutePath(s.pathPrefix, prefix), s.filter()(h))
}

// HandleFunc registers a new route with a matcher for the URL path.
func (s *Server) HandleFunc(path string, h http.HandlerFunc) {
	s.Handle(path, h)
}

// HandleHeader registers a new route with a matcher for the header.
func (s *Server) HandleHeader(key, val string, h http.HandlerFunc) {
	s.router.handleHeader(key, val, s.filter()(h))
}

// ServeHTTP should write reply headers and data to the ResponseWriter and then return.
func (s *Server) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	s.Handler.ServeHTTP(res, req)
}

func (s *Server) filter() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return &serverFilter{server: s, next: next}
	}
}

type serverFilter struct {
	server *Server
	next   http.Handler
}

func (f *serverFilter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	f.serve(w, req, nil, nil, nil)
}

func (f *serverFilter) serveMatchedRoute(w http.ResponseWriter, req *http.Request, route *compiledRoute, values, captureNames []string) {
	f.serve(w, req, route, values, captureNames)
}

func (f *serverFilter) serve(w http.ResponseWriter, req *http.Request, route *compiledRoute, values, captureNames []string) {
	ctx := req.Context()
	if f.server.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.server.timeout)
		defer cancel()
	}

	pathTemplate := routeTemplate(req)
	if route != nil {
		pathTemplate = route.template
	}
	tr := &Transport{
		operation:    pathTemplate,
		pathTemplate: pathTemplate,
		reqHeader:    headerCarrier(req.Header),
		replyHeader:  headerCarrier(w.Header()),
		request:      req,
		response:     w,
		route:        route,
	}
	if f.server.endpoint != nil {
		tr.endpoint = f.server.endpoint.String()
	}
	tr.request = req.WithContext(transport.NewServerContext(ctx, tr))
	if route != nil {
		route.setPathValues(tr.request, values, captureNames)
	}
	f.next.ServeHTTP(w, tr.request)
}

// Endpoint return a real address to registry endpoint.
// examples:
//
//	https://127.0.0.1:8000
//	Legacy: http://127.0.0.1:8000?isSecure=false
func (s *Server) Endpoint() (*url.URL, error) {
	if err := s.listenAndEndpoint(); err != nil {
		return nil, err
	}
	return s.endpoint, nil
}

// Start start the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	if err := s.listenAndEndpoint(); err != nil {
		return err
	}
	s.BaseContext = func(net.Listener) context.Context {
		return ctx
	}
	log.Info("[HTTP] server listening", "addr", s.lis.Addr().String())
	s.serving.Store(true)
	defer s.serving.Store(false)
	var err error
	if s.tlsConf != nil {
		err = s.ServeTLS(s.lis, "", "")
	} else {
		err = s.Serve(s.lis)
	}
	if !stderrors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Healthz reports whether the server accepts new requests: true while the
// listener serves, false before Start and as soon as a stop begins.
func (s *Server) Healthz() bool {
	return s.serving.Load()
}

// GracefulStop stops accepting new connections and waits for in-flight
// requests to finish. When ctx ends first it returns the context's error and
// leaves open connections alone; the caller decides whether to force
// termination with Stop.
func (s *Server) GracefulStop(ctx context.Context) error {
	s.serving.Store(false)
	log.Info("[HTTP] server stopping")
	return s.Shutdown(ctx)
}

// Stop stop the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.serving.Store(false)
	log.Info("[HTTP] server stopping")
	err := s.Shutdown(ctx)
	if err != nil {
		if ctx.Err() != nil {
			log.Warn("[HTTP] server couldn't stop gracefully in time, doing force stop")
			err = s.Close()
		}
	}
	return err
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
		s.endpoint = endpoint.NewEndpoint(endpoint.Scheme("http", s.tlsConf != nil), addr)
	}
	return s.err
}
