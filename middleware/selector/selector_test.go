package selector

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

var _ transport.Transporter = (*Transport)(nil)

type Transport struct {
	kind      transport.Kind
	endpoint  string
	operation string
	headers   *mockHeader
}

func (tr *Transport) Kind() transport.Kind {
	return tr.kind
}

func (tr *Transport) Endpoint() string {
	return tr.endpoint
}

func (tr *Transport) Operation() string {
	return tr.operation
}

func (tr *Transport) RequestHeader() transport.Header {
	return tr.headers
}

func (tr *Transport) ReplyHeader() transport.Header {
	return nil
}

type mockHeader struct {
	m map[string][]string
}

func (m *mockHeader) Get(key string) string {
	vals := m.m[key]
	if len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func (m *mockHeader) Set(key, value string) {
	m.m[key] = []string{value}
}

func (m *mockHeader) Add(key, value string) {
	m.m[key] = append(m.m[key], value)
}

func (m *mockHeader) Keys() []string {
	keys := make([]string, 0, len(m.m))
	for k := range m.m {
		keys = append(keys, k)
	}
	return keys
}

func (m *mockHeader) Values(key string) []string {
	return m.m[key]
}

func TestMatch(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{
			name: "/hello/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hello/world"}),
			want: true,
		},
		{
			name: "/hi/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hi/world"}),
		},
		{
			name: "/test/1234",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/test/1234"}),
			want: true,
		},
		{
			name: "/example/forge",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/example/forge"}),
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			markApplied := func(handler middleware.UnaryHandler) middleware.UnaryHandler {
				return func(ctx context.Context, req any) (any, error) {
					applied = true
					return handler(ctx, req)
				}
			}
			next := func(_ context.Context, _ any) (any, error) {
				return "reply", nil
			}
			m, err := Client(markApplied).Prefix("/hello/").Regex(`/test/[0-9]+`).
				Path("/example/forge").Build()
			if err != nil {
				t.Fatal(err)
			}
			next = m(next)
			reply, err := next(test.ctx, test.name)
			if err != nil {
				t.Fatal(err)
			}
			if reply != "reply" {
				t.Fatalf("reply = %v, want reply", reply)
			}
			if applied != test.want {
				t.Fatalf("middleware applied = %v, want %v", applied, test.want)
			}
		})
	}
}

func TestMatchClient(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
		want bool
	}{
		{
			name: "/hello/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hello/world"}),
			want: true,
		},
		{
			name: "/hi/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hi/world"}),
		},
		{
			name: "/test/1234",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/test/1234"}),
			want: true,
		},
		{
			name: "/example/forge",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/example/forge"}),
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			applied := false
			markApplied := func(handler middleware.UnaryHandler) middleware.UnaryHandler {
				return func(ctx context.Context, req any) (any, error) {
					applied = true
					return handler(ctx, req)
				}
			}
			next := func(_ context.Context, _ any) (any, error) {
				return "reply", nil
			}
			m, err := Client(markApplied).Prefix("/hello/").Regex(`/test/[0-9]+`).
				Path("/example/forge").Build()
			if err != nil {
				t.Fatal(err)
			}
			next = m(next)
			reply, err := next(test.ctx, test.name)
			if err != nil {
				t.Fatal(err)
			}
			if reply != "reply" {
				t.Fatalf("reply = %v, want reply", reply)
			}
			if applied != test.want {
				t.Fatalf("middleware applied = %v, want %v", applied, test.want)
			}
		})
	}
}

func TestFunc(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "/hello.Update/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hello.Update/world"}),
		},
		{
			name: "/hi.Create/world",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/hi.Create/world"}),
		},
		{
			name: "/test.Name/1234",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/test.Name/1234"}),
		},
		{
			name: "/go-kratos.dev/kratos",
			ctx:  transport.NewClientContext(context.Background(), &Transport{operation: "/go-kratos.dev/kratos"}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := func(_ context.Context, req any) (any, error) {
				t.Log(req)
				return "reply", nil
			}
			m, err := Client(testMiddleware).Match(func(_ context.Context, operation string) bool {
				if strings.HasPrefix(operation, "/go-kratos.dev") || strings.HasSuffix(operation, "world") {
					return true
				}
				return false
			}).Build()
			if err != nil {
				t.Fatal(err)
			}
			next = m(next)
			reply, err := next(test.ctx, test.name)
			if err != nil {
				t.Errorf("expect error is nil, but got %v", err)
			}
			if !reflect.DeepEqual(reply, "reply") {
				t.Errorf("expect reply is reply,but got %v", reply)
			}
		})
	}
}

func TestHeaderFunc(t *testing.T) {
	tests := []struct {
		name string
		ctx  context.Context
	}{
		{
			name: "/hello.Update/world",
			ctx: transport.NewClientContext(context.Background(), &Transport{
				operation: "/hello.Update/world",
				headers:   &mockHeader{map[string][]string{"X-Test": {"test"}}},
			}),
		},
		{
			name: "/hi.Create/world",
			ctx: transport.NewClientContext(context.Background(), &Transport{
				operation: "/hi.Create/world",
				headers:   &mockHeader{map[string][]string{"X-Test": {"test2"}, "go-kratos": {"forge"}}},
			}),
		},
		{
			name: "/test.Name/1234",
			ctx: transport.NewClientContext(context.Background(), &Transport{
				operation: "/test.Name/1234",
				headers:   &mockHeader{map[string][]string{"X-Test": {"test3"}}},
			}),
		},
		{
			name: "/go-kratos.dev/kratos",
			ctx: transport.NewClientContext(context.Background(), &Transport{
				operation: "/go-kratos.dev/kratos",
				headers:   &mockHeader{map[string][]string{"X-Test": {"test"}}},
			}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := func(_ context.Context, req any) (any, error) {
				t.Log(req)
				return "reply", nil
			}
			m, err := Client(testMiddleware).Match(func(ctx context.Context, _ string) bool {
				tr, ok := transport.FromClientContext(ctx)
				if !ok {
					return false
				}
				if tr.RequestHeader().Get("X-Test") == "test" {
					return true
				}
				if tr.RequestHeader().Get("go-kratos") == "forge" {
					return true
				}
				return false
			}).Build()
			if err != nil {
				t.Fatal(err)
			}
			next = m(next)
			reply, err := next(test.ctx, test.name)
			if err != nil {
				t.Errorf("expect error is nil, but got %v", err)
			}
			if !reflect.DeepEqual(reply, "reply") {
				t.Errorf("expect reply is reply,but got %v", reply)
			}
		})
	}
}

func testMiddleware(handler middleware.UnaryHandler) middleware.UnaryHandler {
	return func(ctx context.Context, req any) (reply any, err error) {
		reply, err = handler(ctx, req)
		return
	}
}

func Test_RegexMatch(t *testing.T) {
	tests := []struct {
		name      string
		regex     []string
		operation string
		want      bool
	}{
		{
			name:      "exact match with digits",
			regex:     []string{`/test/[0-9]+`},
			operation: "/test/1234",
			want:      true,
		},
		{
			name:      "no match",
			regex:     []string{`/test/[0-9]+`},
			operation: "/test/abc",
			want:      false,
		},
		{
			name:      "multiple patterns first matches",
			regex:     []string{`/api/v[0-9]+/.*`, `/test/.*`},
			operation: "/api/v2/users",
			want:      true,
		},
		{
			name:      "multiple patterns second matches",
			regex:     []string{`/api/v[0-9]+/.*`, `/test/.*`},
			operation: "/test/hello",
			want:      true,
		},
		{
			name:      "multiple patterns none match",
			regex:     []string{`/api/v[0-9]+/.*`, `/test/[0-9]+`},
			operation: "/other/path",
			want:      false,
		},
		{
			name:      "empty regex list",
			regex:     []string{},
			operation: "/test/1234",
			want:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var middlewareApplied bool
			markMiddleware := func(handler middleware.UnaryHandler) middleware.UnaryHandler {
				return func(ctx context.Context, req any) (any, error) {
					middlewareApplied = true
					return handler(ctx, req)
				}
			}
			next := func(_ context.Context, _ any) (any, error) {
				return "reply", nil
			}
			ctx := transport.NewClientContext(context.Background(), &Transport{operation: tt.operation})
			m, err := Client(markMiddleware).Regex(tt.regex...).Build()
			if err != nil {
				t.Fatal(err)
			}
			handler := m(next)
			_, _ = handler(ctx, tt.operation)
			if middlewareApplied != tt.want {
				t.Errorf("middleware applied = %v, want %v", middlewareApplied, tt.want)
			}
		})
	}
}

func Test_InvalidRegexFailsBuild(t *testing.T) {
	m, err := Client(testMiddleware).Regex("^\b(?", `/valid/[0-9]+`).Build()
	if err == nil {
		t.Fatal("Build() error = nil, want error for invalid regex")
	}
	if m != nil {
		t.Errorf("Build() middleware = %v, want nil on error", m)
	}
}

func Test_matches(t *testing.T) {
	m, err := newMatcher(nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.matches(context.Background(), func(_ context.Context) (transport.Transporter, bool) { return nil, false }) {
		t.Error("The matches method must return false.")
	}
}

// TestSelectorComposesOnce asserts the Request-Path Contract: the selected
// chain is composed when the middleware wraps its handler, not per request.
func TestSelectorComposesOnce(t *testing.T) {
	var compositions, calls int
	m := func(next middleware.UnaryHandler) middleware.UnaryHandler {
		compositions++
		return func(ctx context.Context, req any) (any, error) {
			calls++
			return next(ctx, req)
		}
	}

	built, err := Client(m).Prefix("/example").Build()
	if err != nil {
		t.Fatal(err)
	}
	handler := built(func(_ context.Context, _ any) (any, error) {
		return "reply", nil
	})

	ctx := transport.NewClientContext(context.Background(), &Transport{operation: "/example/forge"})
	for range 3 {
		if _, err := handler(ctx, nil); err != nil {
			t.Fatal(err)
		}
	}

	if compositions != 1 {
		t.Errorf("middleware compositions = %d, want 1", compositions)
	}
	if calls != 3 {
		t.Errorf("middleware calls = %d, want 3", calls)
	}
}
