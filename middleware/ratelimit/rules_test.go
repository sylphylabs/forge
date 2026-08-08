package ratelimit

import (
	"context"
	"errors"
	"testing"

	"github.com/sylphylabs/forge/middleware/governance"
	"github.com/sylphylabs/forge/transport"
)

var _ transport.Transporter = (*testTransport)(nil)

type testTransport struct {
	operation string
}

func (tr *testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (tr *testTransport) Endpoint() string                { return "" }
func (tr *testTransport) Operation() string               { return tr.operation }
func (tr *testTransport) RequestHeader() transport.Header { return nil }

type allowLimiter struct{}

func (allowLimiter) Allow() (DoneFunc, error) { return func(DoneInfo) {}, nil }

type denyLimiter struct{}

func (denyLimiter) Allow() (DoneFunc, error) { return nil, errors.New("denied") }

func serverCtx(operation string) context.Context {
	return transport.NewServerContext(context.Background(), &testTransport{operation: operation})
}

func TestServerWithRulesHotUpdate(t *testing.T) {
	rules := governance.NewRules[Limiter](allowLimiter{})
	next := func(context.Context, any) (any, error) { return "ok", nil }
	handler := Server(WithRules(rules))(next)

	if _, err := handler(serverCtx("/svc/Method"), nil); err != nil {
		t.Fatalf("default rule must allow, got %v", err)
	}

	// A snapshot swap changes behavior on the already-built handler chain.
	rules.Replace(map[string]Limiter{"/svc/Method": denyLimiter{}})
	if _, err := handler(serverCtx("/svc/Method"), nil); !errors.Is(err, ErrLimitExceed) {
		t.Fatalf("updated rule must reject, got %v", err)
	}

	// Operations without an exact rule keep the fallback.
	if _, err := handler(serverCtx("/svc/Other"), nil); err != nil {
		t.Fatalf("unmatched operation must use fallback, got %v", err)
	}

	// The wildcard rule governs every unmatched operation.
	rules.Replace(map[string]Limiter{governance.Wildcard: denyLimiter{}})
	if _, err := handler(serverCtx("/svc/Other"), nil); !errors.Is(err, ErrLimitExceed) {
		t.Fatalf("wildcard rule must reject, got %v", err)
	}

	// Outside a transport context the fallback applies; nothing panics.
	if _, err := handler(context.Background(), nil); !errors.Is(err, ErrLimitExceed) {
		t.Fatalf("no-transport call must use fallback, got %v", err)
	}
}

func TestServerWithRulesNilLimiterFallsBack(t *testing.T) {
	// A table that yields nil must fall back to the static limiter rather
	// than dereference nil.
	rules := governance.NewRules[Limiter](nil)
	next := func(context.Context, any) (any, error) { return "ok", nil }
	handler := Server(WithRules(rules), WithLimiter(allowLimiter{}))(next)
	if _, err := handler(serverCtx("/svc/Method"), nil); err != nil {
		t.Fatalf("nil rule must fall back to the static limiter, got %v", err)
	}
}

func TestParseRule(t *testing.T) {
	valid := map[string]any{"window": "5s", "bucket": 50, "cpu_threshold": 900}
	l, err := ParseRule(newTestValue(valid))
	if err != nil {
		t.Fatalf("valid rule must parse, got %v", err)
	}
	if l == nil {
		t.Fatal("valid rule must yield a limiter")
	}

	// An empty rule keeps every default and still yields a limiter.
	if l, err := ParseRule(newTestValue(map[string]any{})); err != nil || l == nil {
		t.Fatalf("empty rule must yield a default limiter, got %v, %v", l, err)
	}

	invalid := []map[string]any{
		{"window": "not-a-duration"},
		{"window": "-1s"},
		{"window": "0s"},
		{"bucket": -1},
		{"cpu_threshold": -1},
		{"cpu_threshold": 1001},
	}
	for _, rule := range invalid {
		if _, err := ParseRule(newTestValue(rule)); err == nil {
			t.Errorf("rule %v must be rejected", rule)
		}
	}
}
