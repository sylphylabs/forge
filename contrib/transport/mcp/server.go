package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"

	"github.com/mark3labs/mcp-go/server"
)

// ErrUnexpectedReply reports framework middleware that replaced a tool result
// with a value the MCP transport cannot return.
var ErrUnexpectedReply = errors.New("mcp: middleware returned a reply that is not *mcp.CallToolResult")

var (
	_ transport.Server     = (*Server)(nil)
	_ transport.Endpointer = (*Server)(nil)
	_ http.Handler         = (*Server)(nil)
)

// MiddlewareFunc is a function that takes an http.Handler and returns an http.Handler.
type MiddlewareFunc func(http.Handler) http.Handler

// ServerOption is an HTTP server option.
type ServerOption func(*Server)

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

// WithMiddleware attaches server middleware. Repeated calls append, and the first
// middleware is the outermost wrapper. Nil middleware is ignored.
func WithMiddleware(m ...MiddlewareFunc) ServerOption {
	return func(s *Server) {
		for _, mw := range m {
			if mw != nil {
				s.middleware = append(s.middleware, mw)
			}
		}
	}
}

// WithSrvOptions appends raw MCP server options.
func WithSrvOptions(opts ...server.ServerOption) ServerOption {
	return func(s *Server) {
		s.srvOpts = append(s.srvOpts, opts...)
	}
}

// WithSSEOptions appends raw SSE server options.
func WithSSEOptions(opts ...server.SSEOption) ServerOption {
	return func(s *Server) {
		s.sseOpts = append(s.sseOpts, opts...)
	}
}

// Server is a MCP server.
type Server struct {
	*server.MCPServer
	srv             *http.Server
	sse             *server.SSEServer
	middleware      []MiddlewareFunc
	unaryMiddleware []middleware.UnaryMiddleware
	address         string
	endpoint        *url.URL
	srvOpts         []server.ServerOption
	sseOpts         []server.SSEOption
}

// chainHTTP composes m in declaration order, so the first middleware is the
// outermost wrapper and runs first on entry.
func chainHTTP(next http.Handler, m ...MiddlewareFunc) http.Handler {
	for i := len(m) - 1; i >= 0; i-- {
		next = m[i](next)
	}
	return next
}

// NewServer creates a new MCP server.
func NewServer(name, version string, opts ...ServerOption) *Server {
	srv := &Server{
		address: ":8000",
	}
	for _, o := range opts {
		o(srv)
	}
	srvOpts := srv.srvOpts
	if len(srv.unaryMiddleware) > 0 {
		// Registered after the caller's own options so that the endpoint,
		// which options may set, is already known.
		endpoint := ""
		if e, err := srv.Endpoint(); err == nil {
			endpoint = e.String()
		}
		srvOpts = append(srvOpts, server.WithToolHandlerMiddleware(
			UnaryMiddleware(endpoint, srv.unaryMiddleware...),
		))
	}
	srv.MCPServer = server.NewMCPServer(name, version, srvOpts...)
	srv.srv = &http.Server{Addr: srv.address, Handler: chainHTTP(srv, srv.middleware...)}
	srv.sse = server.NewSSEServer(srv.MCPServer, append(srv.sseOpts, server.WithHTTPServer(srv.srv))...)
	return srv
}

// ServeHTTP implements the http.Handler interface.
func (s *Server) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	s.sse.ServeHTTP(res, req)
}

// Endpoint return a real address to registry endpoint.
// examples:
// - http://127.0.0.1:8000
func (s *Server) Endpoint() (*url.URL, error) {
	if s.endpoint != nil {
		return s.endpoint, nil
	}
	return url.Parse(fmt.Sprintf("http://%s", s.address))
}

// Start start the MCP server.
func (s *Server) Start(_ context.Context) error {
	if err := s.srv.ListenAndServe(); err != nil {
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	return nil
}

// Stop stop the MCP server.
func (s *Server) Stop(ctx context.Context) error {
	return s.sse.Shutdown(ctx)
}
