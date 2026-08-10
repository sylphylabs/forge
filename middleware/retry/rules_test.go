package retry

import (
	"testing"
	"time"
)

func TestParseRule(t *testing.T) {
	valid := map[string]any{"attempts": 4, "base_backoff": "50ms", "max_backoff": "2s"}
	p, err := ParseRule(newTestValue(valid))
	if err != nil {
		t.Fatalf("valid rule must parse, got %v", err)
	}
	want := Policy{Attempts: 4, BaseBackoff: 50 * time.Millisecond, MaxBackoff: 2 * time.Second}
	if p != want {
		t.Fatalf("want %+v, got %+v", want, p)
	}

	// An empty rule keeps every default and still yields a policy.
	if empty, err := ParseRule(newTestValue(map[string]any{})); err != nil || empty != defaultPolicy {
		t.Fatalf("empty rule must yield the default policy, got %+v, %v", empty, err)
	}

	// A partial rule keeps the defaults of the fields it does not name.
	p, err = ParseRule(newTestValue(map[string]any{"attempts": 5}))
	if err != nil {
		t.Fatalf("partial rule must parse, got %v", err)
	}
	if p.Attempts != 5 || p.BaseBackoff != defaultPolicy.BaseBackoff || p.MaxBackoff != defaultPolicy.MaxBackoff {
		t.Fatalf("partial rule must keep defaults, got %+v", p)
	}

	// attempts: 1 disables retries and needs no backoff fields.
	if p, err := ParseRule(newTestValue(map[string]any{"attempts": 1})); err != nil || p.Attempts != 1 {
		t.Fatalf("attempts=1 must parse, got %+v, %v", p, err)
	}

	invalid := []map[string]any{
		{"attempts": -1},
		{"base_backoff": "not-a-duration"},
		{"base_backoff": "-10ms"},
		{"base_backoff": "0s"},
		{"max_backoff": "10ms"}, // below the default base of 100ms
		{"attempts": 3, "base_backoff": "1s", "max_backoff": "100ms"},
	}
	for _, rule := range invalid {
		if _, err := ParseRule(newTestValue(rule)); err == nil {
			t.Errorf("rule %v must be rejected", rule)
		}
	}
}
