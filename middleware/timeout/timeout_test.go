package timeout

import (
	"context"
	"testing"
	"time"

	"github.com/sylphylabs/forge/errors"
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

func serverCtx(operation string) context.Context {
	return transport.NewServerContext(context.Background(), &testTransport{operation: operation})
}

// waitForDeadline blocks until the request context ends, as a well-behaved
// slow handler would.
func waitForDeadline(ctx context.Context, _ any) (any, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func instant(context.Context, any) (any, error) { return "ok", nil }

func TestServerAppliesDeadline(t *testing.T) {
	handler := Server(WithTimeout(10 * time.Millisecond))(waitForDeadline)
	if _, err := handler(context.Background(), nil); errors.KindOf(err) != errors.KindDeadlineExceeded {
		t.Fatalf("slow handler must time out with ErrTimeout, got %v", err)
	}

	handler = Server(WithTimeout(time.Second))(instant)
	if _, err := handler(context.Background(), nil); err != nil {
		t.Fatalf("fast handler must pass, got %v", err)
	}
}

func TestServerZeroDisables(t *testing.T) {
	handler := Server(WithTimeout(0))(func(ctx context.Context, _ any) (any, error) {
		if _, ok := ctx.Deadline(); ok {
			t.Error("zero timeout must not set a deadline")
		}
		return "ok", nil
	})
	if _, err := handler(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestServerKeepsEarlierDeadline(t *testing.T) {
	// An earlier deadline already on the context wins and its error is not
	// remapped to ErrTimeout: this middleware did not cause the miss.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	handler := Server(WithTimeout(time.Minute))(waitForDeadline)
	_, err := handler(ctx, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want context.DeadlineExceeded, got %v", err)
	}
	if errors.KindOf(err) == errors.KindDeadlineExceeded {
		t.Fatal("an inherited deadline miss must not be remapped to ErrTimeout")
	}
}

func TestServerWithRulesHotUpdate(t *testing.T) {
	rules := governance.NewRules[time.Duration](time.Minute)
	handler := Server(WithRules(rules))(waitForDeadline)

	// Tighten one operation at runtime; the built chain observes it.
	rules.Replace(map[string]time.Duration{"/svc/Slow": 10 * time.Millisecond})
	if _, err := handler(serverCtx("/svc/Slow"), nil); errors.KindOf(err) != errors.KindDeadlineExceeded {
		t.Fatalf("tightened rule must time out, got %v", err)
	}

	// The wildcard rule reaches every other operation.
	rules.Replace(map[string]time.Duration{governance.Wildcard: 10 * time.Millisecond})
	if _, err := handler(serverCtx("/svc/Other"), nil); errors.KindOf(err) != errors.KindDeadlineExceeded {
		t.Fatalf("wildcard rule must time out, got %v", err)
	}

	// A zero lookup falls back to the static deadline rather than
	// accidentally disabling the timeout.
	zero := governance.NewRules[time.Duration](0)
	fast := Server(WithRules(zero), WithTimeout(10*time.Millisecond))(waitForDeadline)
	if _, err := fast(serverCtx("/svc/Slow"), nil); errors.KindOf(err) != errors.KindDeadlineExceeded {
		t.Fatalf("zero rule must fall back to the static deadline, got %v", err)
	}
}

func TestParseRule(t *testing.T) {
	d, err := ParseRule(newTestValue("300ms"))
	if err != nil || d != 300*time.Millisecond {
		t.Fatalf("want 300ms, got %v, %v", d, err)
	}
	for _, raw := range []any{"not-a-duration", "-1s", "0s", map[string]any{}} {
		if _, err := ParseRule(newTestValue(raw)); err == nil {
			t.Errorf("rule %v must be rejected", raw)
		}
	}
}
