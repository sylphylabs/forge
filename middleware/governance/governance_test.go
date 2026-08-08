package governance

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sylphylabs/forge/config"
)

// memorySource is a config.Source whose payload can be swapped at runtime and
// whose watcher can be signaled, driving the real config reload pipeline.
type memorySource struct {
	mu   sync.Mutex
	data string
	sig  chan struct{}
}

func newMemorySource(data string) *memorySource {
	return &memorySource{data: data, sig: make(chan struct{})}
}

func (s *memorySource) Load() ([]*config.KeyValue, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []*config.KeyValue{{Key: "memory", Value: []byte(s.data), Format: "json"}}, nil
}

func (s *memorySource) Watch() (config.Watcher, error) {
	return &memoryWatcher{sig: s.sig, exit: make(chan struct{})}, nil
}

func (s *memorySource) update(data string) {
	s.mu.Lock()
	s.data = data
	s.mu.Unlock()
	s.sig <- struct{}{}
}

type memoryWatcher struct {
	sig  chan struct{}
	exit chan struct{}
}

func (w *memoryWatcher) Next() ([]*config.KeyValue, error) {
	select {
	case <-w.sig:
		return nil, nil
	case <-w.exit:
		return nil, errors.New("watcher stopped")
	}
}

func (w *memoryWatcher) Stop() error {
	close(w.exit)
	return nil
}

// parsePositive parses an int rule and rejects negatives, standing in for a
// real governance parameter such as a rate-limit threshold.
func parsePositive(v config.Value) (int64, error) {
	i, err := v.Int()
	if err != nil {
		return 0, err
	}
	if i < 0 {
		return 0, fmt.Errorf("must not be negative, got %d", i)
	}
	return i, nil
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached before deadline")
}

func TestRulesLookup(t *testing.T) {
	r := NewRules[int64](7)
	if got := r.For("/svc/Method"); got != 7 {
		t.Fatalf("empty table must fall back to default, got %d", got)
	}

	r.Replace(map[string]int64{
		"/svc/Method": 1,
		Wildcard:      2,
	})
	if got := r.For("/svc/Method"); got != 1 {
		t.Fatalf("exact match want 1, got %d", got)
	}
	if got := r.For("/svc/Other"); got != 2 {
		t.Fatalf("wildcard fallback want 2, got %d", got)
	}

	// Without a wildcard entry, unmatched operations get the default.
	r.Replace(map[string]int64{"/svc/Method": 3})
	if got := r.For("/svc/Other"); got != 7 {
		t.Fatalf("default fallback want 7, got %d", got)
	}

	// An empty snapshot resets to defaults; no stale rules survive.
	r.Replace(nil)
	if got := r.For("/svc/Method"); got != 7 {
		t.Fatalf("reset must drop old rules, got %d", got)
	}
}

func TestRulesOperationIsOpaque(t *testing.T) {
	r := NewRules[int64](0)
	// Operation strings with dots and slashes are plain map keys, never
	// interpreted as config paths or patterns.
	r.Replace(map[string]int64{"/helloworld.Greeter/SayHello": 42})
	if got := r.For("/helloworld.Greeter/SayHello"); got != 42 {
		t.Fatalf("dotted operation must match exactly, got %d", got)
	}
	if got := r.For("/helloworld.Greeter"); got != 0 {
		t.Fatalf("no prefix or structural matching allowed, got %d", got)
	}
}

func TestRulesConcurrency(t *testing.T) {
	r := NewRules[int64](1)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if v := r.For("/svc/Method"); v != 1 && v != 2 {
					panic("observed a value outside any snapshot")
				}
				_ = r.For("/svc/Other")
			}
		}()
	}
	for i := range 1000 {
		r.Replace(map[string]int64{"/svc/Method": int64(2 - i%2)})
	}
	close(done)
	wg.Wait()
}

func TestWatchHotUpdate(t *testing.T) {
	src := newMemorySource(`{"governance":{"ratelimit":{"*":100,"/svc/Method":10}}}`)
	c := config.New(config.WithSource(src))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rules := NewRules[int64](0)
	if err := Watch(c, "governance.ratelimit", rules, parsePositive); err != nil {
		t.Fatal(err)
	}

	// Initial snapshot is installed synchronously.
	if got := rules.For("/svc/Method"); got != 10 {
		t.Fatalf("initial exact rule want 10, got %d", got)
	}
	if got := rules.For("/svc/Other"); got != 100 {
		t.Fatalf("initial wildcard rule want 100, got %d", got)
	}

	// A source update flows through the watch pipeline without a restart.
	src.update(`{"governance":{"ratelimit":{"*":200,"/svc/Method":20}}}`)
	eventually(t, func() bool {
		return rules.For("/svc/Method") == 20 && rules.For("/svc/Other") == 200
	})

	// Removing the exact rule falls back to the new wildcard.
	src.update(`{"governance":{"ratelimit":{"*":300}}}`)
	eventually(t, func() bool { return rules.For("/svc/Method") == 300 })
}

func TestWatchRejectsInvalidSnapshot(t *testing.T) {
	src := newMemorySource(`{"governance":{"ratelimit":{"*":100}}}`)
	c := config.New(config.WithSource(src))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rules := NewRules[int64](0)
	if err := Watch(c, "governance.ratelimit", rules, parsePositive); err != nil {
		t.Fatal(err)
	}

	// One bad rule poisons the whole snapshot: the valid "*" change must not
	// apply either, and the previous rules stay in effect.
	src.update(`{"governance":{"ratelimit":{"*":999,"/svc/Method":-1}}}`)

	// Prove the update was seen and rejected — not still queued — by pushing
	// a later valid snapshot through the same pipeline.
	src.update(`{"governance":{"ratelimit":{"*":400}}}`)
	eventually(t, func() bool { return rules.For("/svc/Other") == 400 })
	if got := rules.For("/svc/Method"); got != 400 {
		t.Fatalf("rejected snapshot must not leak rules, got %d", got)
	}
}

func TestWatchRejectsMalformedSection(t *testing.T) {
	src := newMemorySource(`{"governance":{"ratelimit":{"*":100}}}`)
	c := config.New(config.WithSource(src))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	rules := NewRules[int64](0)
	if err := Watch(c, "governance.ratelimit", rules, parsePositive); err != nil {
		t.Fatal(err)
	}

	// The section flips from a map to a scalar: rejected wholesale, old
	// rules retained. A later valid snapshot still applies.
	src.update(`{"governance":{"ratelimit":"broken"}}`)
	src.update(`{"governance":{"ratelimit":{"*":500}}}`)
	eventually(t, func() bool { return rules.For("/svc/Other") == 500 })
}

func TestWatchInitialErrors(t *testing.T) {
	src := newMemorySource(`{"governance":{"ratelimit":{"/svc/Method":-5}}}`)
	c := config.New(config.WithSource(src))
	if err := c.Load(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// Invalid initial rules must fail loudly, not install a partial table.
	if err := Watch(c, "governance.ratelimit", NewRules[int64](0), parsePositive); err == nil {
		t.Fatal("invalid initial snapshot must return an error")
	}

	// A missing section is a wiring mistake, not an empty rule set.
	if err := Watch(c, "governance.nosuch", NewRules[int64](0), parsePositive); err == nil {
		t.Fatal("missing section must return an error")
	}

	// Nil arguments are refused up front.
	if err := Watch[int64](nil, "k", nil, nil); err == nil {
		t.Fatal("nil arguments must return an error")
	}
}
