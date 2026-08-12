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

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/backstop"
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
	_ http.Handler              = (*Server)(nil)
)

// ErrServerStopped is returned by Start on a server that was already stopped.
// A Server serves once: Stop and GracefulStop close the listener for good, so
// a restart needs a new Server.
var ErrServerStopped = stderrors.New("http: server already stopped")

// ServerOption is an HTTP server option.
type ServerOption func(*Server)

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

// WithFilter attaches HTTP filters that run around the whole handler chain.
func WithFilter(filters ...FilterFunc) ServerOption {
	return func(o *Server) {
		o.filters = filters
	}
}

// WithMiddleware attaches server-wide unary middleware. It is composed once,
// inside NewServer, and runs outside (before) any middleware attached through
// a generated service plan. A nil middleware, or one returning a nil handler,
// is reported by Start and Endpoint, the way a bad listener is.
//
// It runs after routing, so [transport.FromServerContext] works, but before
// the request body is decoded: the handler's request argument is the
// [*http.Request], and the reply is always nil. Middleware that needs the
// decoded message belongs in the generated per-service plan.
//
// There is no server-wide stream middleware for HTTP: an HTTP stream is
// created by the handler itself (SSE or WebSocket upgrade), after this layer
// has already run, so the transport has no stream lifecycle to wrap.
//
// The client option of the same intent is [WithClientMiddleware].
func WithMiddleware(ms ...middleware.UnaryMiddleware) ServerOption {
	return func(s *Server) {
		s.middleware = append(s.middleware, ms...)
	}
}

// WithRequestVarsDecoder sets the decoder for path variables.
func WithRequestVarsDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decVars = dec
	}
}

// WithRequestQueryDecoder sets the decoder for query parameters.
func WithRequestQueryDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decQuery = dec
	}
}

// WithRequestDecoder sets the decoder for request bodies.
func WithRequestDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decBody = dec
	}
}

// WithResponseEncoder sets the response encoder.
func WithResponseEncoder(en EncodeResponseFunc) ServerOption {
	return func(o *Server) {
		o.enc = en
	}
}

// WithErrorEncoder sets the error encoder.
func WithErrorEncoder(en EncodeErrorFunc) ServerOption {
	return func(o *Server) {
		o.ene = en
	}
}

// WithTLSConfig sets the server TLS config.
func WithTLSConfig(c *tls.Config) ServerOption {
	return func(o *Server) {
		o.tlsConf = c
	}
}

// WithListener sets the listener the server serves on.
func WithListener(lis net.Listener) ServerOption {
	return func(s *Server) {
		s.lis = lis
	}
}

// WithPathPrefix applies a common prefix to routes registered on the server.
func WithPathPrefix(prefix string) ServerOption {
	return func(s *Server) {
		s.pathPrefix = prefix
	}
}

// WithNotFoundHandler sets the handler invoked when no route matches.
func WithNotFoundHandler(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.router.setNotFoundHandler(handler)
	}
}

// WithMethodNotAllowedHandler sets the handler invoked when a route matches
// the path but not the method.
func WithMethodNotAllowedHandler(handler http.Handler) ServerOption {
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
	middleware []middleware.UnaryMiddleware
	handler    middleware.UnaryHandler
	decVars    DecodeRequestFunc
	decQuery   DecodeRequestFunc
	decBody    DecodeRequestFunc
	enc        EncodeResponseFunc
	ene        EncodeErrorFunc
	pathPrefix string
	router     *routeMux
	serving    atomic.Bool
	stopped    atomic.Bool
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
	srv.composeMiddleware()
	srv.router.setErrorEncoder(srv.ene)
	srv.Server = &http.Server{
		Handler:   &backstopHandler{server: srv, next: FilterChain(srv.filters...)(srv.router)},
		TLSConfig: srv.tlsConf,
	}
	return srv
}

// dispatchKey carries the continuation of one request through the server-wide
// middleware chain. The chain itself is composed once, in NewServer; only the
// matched handler and response writer of the current request travel through
// the context.
type dispatchKey struct{}

type dispatch struct {
	w    http.ResponseWriter
	next http.Handler
}

// errLostDispatch reports server-wide middleware that severed the request from
// its continuation: it called next with a context not derived from the one it
// was given, or with a request that is not the *http.Request.
var errLostDispatch = forgeerrors.MustDefine(forgeerrors.KindInternal, forgeerrors.Domain, "HTTP_DISPATCH")

// composeMiddleware composes the server-wide middleware exactly once, during
// construction. The composed handler closes over a context cell the dispatch
// path fills per request, so composition cost is paid here and never on the
// request path. A compose failure is stored in s.err and surfaces from Start
// and Endpoint, where a bad listener surfaces too.
func (s *Server) composeMiddleware() {
	handler, err := middleware.ComposeUnary(func(ctx context.Context, req any) (any, error) {
		d, ok := ctx.Value(dispatchKey{}).(dispatch)
		if !ok {
			return nil, errLostDispatch
		}
		r, ok := req.(*http.Request)
		if !ok {
			return nil, errLostDispatch.Msgf("server middleware replaced the request with %T, want *http.Request", req)
		}
		if r.Context() != ctx {
			r = r.WithContext(ctx)
		}
		d.next.ServeHTTP(d.w, r)
		return nil, nil
	}, s.middleware...)
	if err != nil {
		s.err = err
		return
	}
	s.handler = handler
}

// backstopHandler is the transport's own recover, outside even server-wide
// middleware: a panic anywhere below is logged with its stack and answered
// with a generic internal error, never the panic value.
type backstopHandler struct {
	server *Server
	next   http.Handler
}

func (h *backstopHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			if rec == http.ErrAbortHandler { //nolint:errorlint // net/http's abort contract compares by identity.
				panic(rec)
			}
			h.server.ene(w, req, backstop.Recovered(req.Context(), "[HTTP]", rec))
		}
	}()
	h.next.ServeHTTP(w, req)
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
	ctx = transport.NewServerContext(ctx, tr)
	ctx = context.WithValue(ctx, dispatchKey{}, dispatch{w: w, next: f.next})
	tr.request = req.WithContext(ctx)
	if route != nil {
		route.setPathValues(tr.request, values, captureNames)
	}
	if _, err := f.server.handler(ctx, tr.request); err != nil {
		f.server.ene(w, tr.request, err)
	}
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
//
// A Server serves once. After Stop or GracefulStop the wrapped http.Server
// refuses to serve again, so a second Start returns [ErrServerStopped] rather
// than reporting a clean shutdown it never performed.
func (s *Server) Start(ctx context.Context) error {
	if s.stopped.Load() {
		return ErrServerStopped
	}
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
	s.stopped.Store(true)
	log.Info("[HTTP] server stopping")
	return s.Shutdown(ctx)
}

// Stop stop the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	s.serving.Store(false)
	s.stopped.Store(true)
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
		s.endpoint = endpoint.NewEndpoint(endpoint.Scheme("http", s.tlsConf != nil), addr)
	}
	return s.err
}
