package diagnosis

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// staticProbe returns a probe that always yields v.
func staticProbe(v any) ProbeFunc {
	return func(context.Context) (any, error) { return v, nil }
}

func TestRegisterAndProbe(t *testing.T) {
	reg := NewRegistry()
	reg.Register("app", staticProbe(map[string]string{"name": "svc"}))

	res, ok := reg.Probe(context.Background(), "app")
	if !ok {
		t.Fatal("registered probe must be found")
	}
	if res.Err != nil {
		t.Fatalf("unexpected probe error: %v", res.Err)
	}
	want := map[string]string{"name": "svc"}
	if !reflect.DeepEqual(res.Value, want) {
		t.Fatalf("probe value = %#v, want %#v", res.Value, want)
	}

	if _, ok := reg.Probe(context.Background(), "missing"); ok {
		t.Fatal("unknown probe must report ok=false")
	}
}

func TestRegisterPanicsOnMisuse(t *testing.T) {
	mustPanic := func(t *testing.T, want string, fn func()) {
		t.Helper()
		defer func() {
			v := recover()
			if v == nil {
				t.Fatal("expected a panic")
			}
			if msg := fmt.Sprint(v); !strings.Contains(msg, want) {
				t.Fatalf("panic message %q does not mention %q", msg, want)
			}
		}()
		fn()
	}

	reg := NewRegistry()
	mustPanic(t, "empty probe name", func() { reg.Register("", staticProbe(nil)) })
	mustPanic(t, "nil ProbeFunc", func() { reg.Register("app", nil) })

	reg.Register("app", staticProbe(nil))
	mustPanic(t, "already registered", func() { reg.Register("app", staticProbe(nil)) })
}

func TestNamesSorted(t *testing.T) {
	reg := NewRegistry()
	for _, name := range []string{"pool/connections", "app", "governance/ratelimit"} {
		reg.Register(name, staticProbe(nil))
	}
	want := []string{"app", "governance/ratelimit", "pool/connections"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
}

func TestCollectIsolatesFailures(t *testing.T) {
	probeErr := errors.New("state unavailable")
	reg := NewRegistry()
	reg.Register("good", staticProbe(42))
	reg.Register("failing", func(context.Context) (any, error) { return nil, probeErr })
	reg.Register("panicking", func(context.Context) (any, error) { panic("boom") })

	results := reg.Collect(context.Background())
	if len(results) != 3 {
		t.Fatalf("Collect returned %d results, want 3", len(results))
	}
	if res := results["good"]; res.Err != nil || res.Value != 42 {
		t.Fatalf("good probe = %+v, want value 42", res)
	}
	if res := results["failing"]; !errors.Is(res.Err, probeErr) {
		t.Fatalf("failing probe error = %v, want %v", res.Err, probeErr)
	}
	res := results["panicking"]
	if res.Err == nil || res.Value != nil {
		t.Fatalf("panicking probe = %+v, want an error and no value", res)
	}
	for _, part := range []string{"panicking", "boom"} {
		if !strings.Contains(res.Err.Error(), part) {
			t.Fatalf("panic error %q does not mention %q", res.Err, part)
		}
	}
}

func TestProbeRecoversPanic(t *testing.T) {
	reg := NewRegistry()
	reg.Register("faulty", func(context.Context) (any, error) {
		var m map[string]int
		m["write"] = 1 // deliberate nil-map write
		return m, nil
	})

	res, ok := reg.Probe(context.Background(), "faulty")
	if !ok {
		t.Fatal("probe must be found")
	}
	if res.Err == nil {
		t.Fatal("a panicking probe must surface as an error result")
	}
}

func TestProbeReceivesContext(t *testing.T) {
	reg := NewRegistry()
	reg.Register("ctx", func(ctx context.Context) (any, error) {
		return nil, ctx.Err()
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, _ := reg.Probe(ctx, "ctx")
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("probe error = %v, want context.Canceled", res.Err)
	}
}

// TestConcurrentRegisterAndRead exercises runtime registration racing with
// reads; run with -race.
func TestConcurrentRegisterAndRead(t *testing.T) {
	reg := NewRegistry()
	reg.Register("static", staticProbe("ok"))

	const writers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := range 50 {
				reg.Register(fmt.Sprintf("late/%d-%d", i, j), staticProbe(j))
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				reg.Names()
				reg.Collect(context.Background())
				if res, ok := reg.Probe(context.Background(), "static"); !ok || res.Err != nil {
					t.Error("static probe must stay readable during registration")
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := len(reg.Names()); got != writers*50+1 {
		t.Fatalf("registry holds %d probes, want %d", got, writers*50+1)
	}
}

// TestSlowProbeDoesNotBlockRegistry verifies probes run outside the lock: a
// probe blocked mid-run must not prevent registration or other reads.
func TestSlowProbeDoesNotBlockRegistry(t *testing.T) {
	reg := NewRegistry()
	entered := make(chan struct{})
	release := make(chan struct{})
	reg.Register("slow", func(context.Context) (any, error) {
		close(entered)
		<-release
		return "done", nil
	})

	done := make(chan Result)
	go func() {
		res, _ := reg.Probe(context.Background(), "slow")
		done <- res
	}()
	<-entered

	// With the slow probe still running, the registry must stay usable.
	reg.Register("during", staticProbe(1))
	if _, ok := reg.Probe(context.Background(), "during"); !ok {
		t.Fatal("registration during a running probe must be visible")
	}

	close(release)
	if res := <-done; res.Err != nil || res.Value != "done" {
		t.Fatalf("slow probe = %+v, want value %q", res, "done")
	}
}
