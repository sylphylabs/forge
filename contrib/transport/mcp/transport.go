package mcp

import (
	"context"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// KindMCP identifies the MCP transport. It is declared here rather than in the
// transport package because transport.Kind is an open type.
const KindMCP transport.Kind = "mcp"

var _ transport.Transporter = (*Transport)(nil)

// Transport reports the tool call in flight to framework middleware.
//
// It intentionally does not implement transport.ReplyHeaderer: a tool result
// travels over an established SSE stream and has no mutable reply header of
// its own.
type Transport struct {
	endpoint  string
	operation string
	header    headerCarrier
}

// Kind returns KindMCP.
func (tr *Transport) Kind() transport.Kind { return KindMCP }

// Endpoint returns the server endpoint.
func (tr *Transport) Endpoint() string { return tr.endpoint }

// Operation returns the name of the tool being called, which is this
// transport's answer to "which call is this". It is opaque to callers.
func (tr *Transport) Operation() string { return tr.operation }

// RequestHeader returns the HTTP headers of the originating request. It is
// empty when the client did not travel over HTTP.
func (tr *Transport) RequestHeader() transport.Header { return tr.header }

// headerCarrier adapts http.Header to transport.Header.
type headerCarrier http.Header

func (h headerCarrier) Get(key string) string { return http.Header(h).Get(key) }

func (h headerCarrier) Set(key string, value string) { http.Header(h).Set(key, value) }

func (h headerCarrier) Add(key string, value string) { http.Header(h).Add(key, value) }

func (h headerCarrier) Values(key string) []string { return http.Header(h).Values(key) }

func (h headerCarrier) Keys() []string {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	return keys
}

// UnaryMiddleware adapts framework middleware to an mcp-go tool handler
// middleware, so an MCP server reuses the same logging, tracing, recovery,
// rate limiting, and metadata as HTTP and gRPC servers.
//
// The handler receives the mcp.CallToolRequest as its request and returns a
// *mcp.CallToolResult as its reply. Middleware that inspects the request type
// should type-assert accordingly.
//
// Pass the result to server.WithToolHandlerMiddleware, or use WithToolMiddleware
// to do both at once.
func UnaryMiddleware(endpoint string, m ...middleware.UnaryMiddleware) server.ToolHandlerMiddleware {
	chain := middleware.ChainUnary(m...)
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			handler := func(ctx context.Context, req any) (any, error) {
				return next(ctx, req.(mcp.CallToolRequest))
			}
			ctx = transport.NewServerContext(ctx, &Transport{
				endpoint:  endpoint,
				operation: request.Params.Name,
				header:    headerCarrier(request.Header),
			})
			reply, err := chain(handler)(ctx, request)
			if err != nil {
				return nil, err
			}
			if reply == nil {
				return nil, nil
			}
			result, ok := reply.(*mcp.CallToolResult)
			if !ok {
				return nil, ErrUnexpectedReply
			}
			return result, nil
		}
	}
}

// WithToolMiddleware registers framework middleware on the server's tool handlers.
// It is the option form of UnaryMiddleware and reports the server's own
// endpoint, so it must be passed to NewServer rather than to SrvOptions.
//
// Repeated calls append. Nil middleware is ignored.
func WithToolMiddleware(m ...middleware.UnaryMiddleware) ServerOption {
	return func(s *Server) {
		for _, mw := range m {
			if mw != nil {
				s.unaryMiddleware = append(s.unaryMiddleware, mw)
			}
		}
	}
}
