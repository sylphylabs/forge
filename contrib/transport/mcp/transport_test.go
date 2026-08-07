package mcp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func callToolRequest(name string) mcpgo.CallToolRequest {
	request := mcpgo.CallToolRequest{Header: http.Header{}}
	request.Params.Name = name
	return request
}

func okHandler(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	return mcpgo.NewToolResultText("ok"), nil
}

// The point of the bridge: framework middleware sees a transport, and its
// Operation is the tool name.
func TestUnaryMiddlewareExposesTransport(t *testing.T) {
	var (
		gotKind      transport.Kind
		gotOperation string
		gotEndpoint  string
		gotHeader    string
	)

	probe := func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				gotKind = tr.Kind()
				gotOperation = tr.Operation()
				gotEndpoint = tr.Endpoint()
				gotHeader = tr.RequestHeader().Get("x-tenant")
			}
			return next(ctx, req)
		}
	}

	request := callToolRequest("hello_world")
	request.Header.Set("x-tenant", "acme")

	handler := UnaryMiddleware("http://127.0.0.1:8000", probe)(okHandler)
	if _, err := handler(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	if gotKind != KindMCP {
		t.Errorf("Kind() = %v, want %v", gotKind, KindMCP)
	}
	if gotOperation != "hello_world" {
		t.Errorf("Operation() = %q, want the tool name", gotOperation)
	}
	if gotEndpoint != "http://127.0.0.1:8000" {
		t.Errorf("Endpoint() = %q, want the server endpoint", gotEndpoint)
	}
	if gotHeader != "acme" {
		t.Errorf("RequestHeader() x-tenant = %q, want %q", gotHeader, "acme")
	}
}

// A tool result travels over an established stream, so there is no reply
// header to expose.
func TestTransportIsNotReplyHeaderer(t *testing.T) {
	var tr transport.Transporter = &Transport{}
	if _, ok := tr.(transport.ReplyHeaderer); ok {
		t.Error("mcp Transport must not implement transport.ReplyHeaderer")
	}
}

func TestUnaryMiddlewareRunsInDeclarationOrder(t *testing.T) {
	var order []string
	record := func(name string) middleware.UnaryMiddleware {
		return func(next middleware.UnaryHandler) middleware.UnaryHandler {
			return func(ctx context.Context, req any) (any, error) {
				order = append(order, name)
				return next(ctx, req)
			}
		}
	}

	handler := UnaryMiddleware("", record("first"), record("second"))(okHandler)
	if _, err := handler(t.Context(), callToolRequest("tool")); err != nil {
		t.Fatal(err)
	}

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("order = %v, want [first second]", order)
	}
}

func TestUnaryMiddlewarePassesResultThrough(t *testing.T) {
	handler := UnaryMiddleware("")(okHandler)
	result, err := handler(t.Context(), callToolRequest("tool"))
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || len(result.Content) == 0 {
		t.Fatal("result was not passed through")
	}
}

func TestUnaryMiddlewarePropagatesHandlerError(t *testing.T) {
	want := errors.New("tool failed")
	handler := UnaryMiddleware("")(func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, want
	})

	if _, err := handler(t.Context(), callToolRequest("tool")); !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

// Recovery and friends short-circuit by returning an error instead of calling
// the tool.
func TestUnaryMiddlewareCanShortCircuit(t *testing.T) {
	want := errors.New("rejected")
	called := false

	reject := func(middleware.UnaryHandler) middleware.UnaryHandler {
		return func(context.Context, any) (any, error) { return nil, want }
	}
	handler := UnaryMiddleware("", reject)(func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		called = true
		return okHandler(context.Background(), mcpgo.CallToolRequest{})
	})

	if _, err := handler(t.Context(), callToolRequest("tool")); !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
	if called {
		t.Error("tool handler ran despite the middleware rejecting the call")
	}
}

func TestUnaryMiddlewareRejectsUnexpectedReply(t *testing.T) {
	replace := func(middleware.UnaryHandler) middleware.UnaryHandler {
		return func(context.Context, any) (any, error) { return "not a result", nil }
	}
	handler := UnaryMiddleware("", replace)(okHandler)

	if _, err := handler(t.Context(), callToolRequest("tool")); !errors.Is(err, ErrUnexpectedReply) {
		t.Errorf("error = %v, want %v", err, ErrUnexpectedReply)
	}
}

func TestUnaryMiddlewareHandlesNilReply(t *testing.T) {
	handler := UnaryMiddleware("")(func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, nil
	})
	result, err := handler(t.Context(), callToolRequest("tool"))
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if result != nil {
		t.Errorf("result = %v, want nil", result)
	}
}

func TestToolMiddlewareAccumulatesAndSkipsNil(t *testing.T) {
	noop := func(next middleware.UnaryHandler) middleware.UnaryHandler { return next }

	srv := &Server{}
	ToolMiddleware(noop, nil)(srv)
	ToolMiddleware(noop)(srv)

	if got := len(srv.unaryMiddleware); got != 2 {
		t.Errorf("unaryMiddleware len = %d, want 2 with nil skipped", got)
	}
}

func TestMiddlewareAccumulatesAndSkipsNil(t *testing.T) {
	noop := func(next http.Handler) http.Handler { return next }

	srv := &Server{}
	Middleware(noop, nil)(srv)
	Middleware(noop)(srv)

	if got := len(srv.middleware); got != 2 {
		t.Errorf("middleware len = %d, want 2 with nil skipped", got)
	}
}

func TestChainHTTPRunsInDeclarationOrder(t *testing.T) {
	var order []string
	record := func(name string) MiddlewareFunc {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	handler := chainHTTP(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		record("first"), record("second"))
	handler.ServeHTTP(nil, nil)

	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("order = %v, want [first second]", order)
	}
}

// ToolMiddleware must reach the tools registered on the server, through
// mcp-go's own middleware registry.
func TestServerToolMiddlewareIsRegistered(t *testing.T) {
	var gotOperation string

	probe := func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				gotOperation = tr.Operation()
			}
			return next(ctx, req)
		}
	}

	srv := NewServer("forge-mcp", "v1.0.0", Address(":8000"), ToolMiddleware(probe))
	srv.AddTool(mcpgo.NewTool("hello_world"), okHandler)

	response := srv.HandleMessage(t.Context(), []byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"hello_world"}}`,
	))
	if response == nil {
		t.Fatal("HandleMessage returned no response")
	}
	if _, isError := response.(mcpgo.JSONRPCError); isError {
		t.Fatalf("HandleMessage returned an error response: %+v", response)
	}

	if gotOperation != "hello_world" {
		t.Errorf("Operation() = %q, want the tool name; middleware was not registered", gotOperation)
	}
}

var _ server.ToolHandlerMiddleware = UnaryMiddleware("")
