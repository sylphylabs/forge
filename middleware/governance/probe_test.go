package governance

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/sylphylabs/forge/diagnosis"
)

func TestProbeReportsCurrentSnapshot(t *testing.T) {
	rules := NewRules[time.Duration](time.Second)
	probe := Probe(rules, nil)

	res, err := probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := res.(map[string]any)
	if got := snapshot[Wildcard]; got != time.Second {
		t.Fatalf("empty table fallback = %v, want 1s", got)
	}
	if len(snapshot) != 1 {
		t.Fatalf("empty table snapshot = %v, want only the wildcard entry", snapshot)
	}

	rules.Replace(map[string]time.Duration{
		"/svc/Slow": 5 * time.Second,
		Wildcard:    300 * time.Millisecond,
	})
	res, err = probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot = res.(map[string]any)
	if got := snapshot["/svc/Slow"]; got != 5*time.Second {
		t.Fatalf("exact rule = %v, want 5s", got)
	}
	if got := snapshot[Wildcard]; got != 300*time.Millisecond {
		t.Fatalf("wildcard rule = %v, want 300ms", got)
	}
}

// limiterRule stands in for a rule value that is not serializable itself — a
// live limiter — and must be projected through describe.
type limiterRule struct{ threshold int }

func TestProbeDescribesOpaqueRules(t *testing.T) {
	rules := NewRules(&limiterRule{threshold: 800})
	rules.Replace(map[string]*limiterRule{
		"/svc/Hot": {threshold: 100},
	})

	probe := Probe(rules, func(r *limiterRule) any {
		return map[string]int{"threshold": r.threshold}
	})
	res, err := probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("described snapshot must serialize: %v", err)
	}
	var decoded map[string]struct {
		Threshold int `json:"threshold"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["/svc/Hot"].Threshold != 100 || decoded[Wildcard].Threshold != 800 {
		t.Fatalf("described snapshot = %s", raw)
	}
}

func TestProbeObservesReplace(t *testing.T) {
	rules := NewRules[int64](1)
	probe := Probe(rules, nil)
	reg := diagnosis.NewRegistry()
	reg.Register("governance/ratelimit", probe)

	rules.Replace(map[string]int64{"/svc/M": 2})
	res, _ := reg.Probe(context.Background(), "governance/ratelimit")
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if got := res.Value.(map[string]any)["/svc/M"]; got != int64(2) {
		t.Fatalf("rule after first Replace = %v, want 2", got)
	}

	rules.Replace(nil) // reset to defaults
	res, _ = reg.Probe(context.Background(), "governance/ratelimit")
	snapshot := res.Value.(map[string]any)
	if _, stale := snapshot["/svc/M"]; stale {
		t.Fatalf("snapshot kept a removed rule: %v", snapshot)
	}
	if got := snapshot[Wildcard]; got != int64(1) {
		t.Fatalf("fallback after reset = %v, want the construction default 1", got)
	}
}

func TestProbeNilRulesPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Probe(nil, nil) must panic")
		}
	}()
	Probe[int](nil, nil)
}
