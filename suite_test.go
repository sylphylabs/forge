package forge

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sylphylabs/forge/registry"
)

// optionsSuite is the simplest possible Suite: a fixed option list.
type optionsSuite []Option

func (s optionsSuite) Options() []Option { return s }

// observabilitySuite is a realistic integration bundle: a configured logger
// plus the service metadata its handlers annotate records with. It knows
// nothing about any other suite in this file.
type observabilitySuite struct {
	logger *slog.Logger
	labels map[string]string
}

func (s *observabilitySuite) Options() []Option {
	return []Option{
		Logger(s.logger),
		Metadata(s.labels),
	}
}

// discoverySuite is a second realistic bundle: a service registrar plus the
// registration timeout it was tuned for. It knows nothing about
// observabilitySuite.
type discoverySuite struct {
	registrar registry.Registrar
	timeout   time.Duration
}

func (s *discoverySuite) Options() []Option {
	return []Option{
		Registrar(s.registrar),
		RegistrarTimeout(s.timeout),
	}
}

func TestWithSuiteAppliesOptions(t *testing.T) {
	o := &options{}
	WithSuite(optionsSuite{ID("1"), Name("svc")})(o)
	if o.id != "1" {
		t.Errorf("o.id = %q, want %q", o.id, "1")
	}
	if o.name != "svc" {
		t.Errorf("o.name = %q, want %q", o.name, "svc")
	}
}

func TestWithSuiteExpandsNestedSuitesInPlace(t *testing.T) {
	var order []string
	record := func(step string) Option {
		return func(*options) { order = append(order, step) }
	}
	inner := optionsSuite{record("inner-1"), record("inner-2")}
	outer := optionsSuite{record("before"), WithSuite(inner), record("after")}

	WithSuite(outer)(&options{})

	want := []string{"before", "inner-1", "inner-2", "after"}
	if len(order) != len(want) {
		t.Fatalf("applied %d options %v, want %d %v", len(order), order, len(want), want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("application order = %v, want %v", order, want)
		}
	}
}

func TestWithSuiteLastOptionWins(t *testing.T) {
	// A directly written option after the suite overrides the suite's value,
	// exactly as it would override an earlier direct option.
	o := &options{}
	for _, opt := range []Option{WithSuite(optionsSuite{Name("from-suite")}), Name("direct")} {
		opt(o)
	}
	if o.name != "direct" {
		t.Errorf("o.name = %q, want %q", o.name, "direct")
	}

	// And symmetrically: a suite applied later overrides a direct option.
	o = &options{}
	for _, opt := range []Option{Name("direct"), WithSuite(optionsSuite{Name("from-suite")})} {
		opt(o)
	}
	if o.name != "from-suite" {
		t.Errorf("o.name = %q, want %q", o.name, "from-suite")
	}
}

func TestWithSuiteTwoIndependentSuitesStack(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	obs := &observabilitySuite{
		logger: logger,
		labels: map[string]string{"team": "platform"},
	}
	reg := &mockRegistrar{}
	disc := &discoverySuite{registrar: reg, timeout: 3 * time.Second}

	app := New(
		WithSuite(obs),
		WithSuite(disc),
		Name("stacked"),
	)

	if app.opts.logger != logger {
		t.Errorf("app.opts.logger = %v, want the suite's logger", app.opts.logger)
	}
	if got := app.Metadata()["team"]; got != "platform" {
		t.Errorf(`Metadata()["team"] = %q, want %q`, got, "platform")
	}
	if app.opts.registrar != reg {
		t.Errorf("app.opts.registrar = %v, want the suite's registrar", app.opts.registrar)
	}
	if app.opts.registrarTimeout != 3*time.Second {
		t.Errorf("app.opts.registrarTimeout = %v, want %v", app.opts.registrarTimeout, 3*time.Second)
	}
	if app.Name() != "stacked" {
		t.Errorf("app.Name() = %q, want %q", app.Name(), "stacked")
	}
}

func TestWithSuiteNilSuitePanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithSuite(nil) did not panic")
		}
		if msg, ok := r.(string); !ok || !strings.Contains(msg, "nil Suite") {
			t.Fatalf("panic = %v, want message containing %q", r, "nil Suite")
		}
	}()
	WithSuite(nil)
}

func TestWithSuiteNilOptionPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("WithSuite with a nil option did not panic")
		}
		msg, ok := r.(string)
		if !ok || !strings.Contains(msg, "nil Option at index 1") {
			t.Fatalf("panic = %v, want message containing %q", r, "nil Option at index 1")
		}
	}()
	WithSuite(optionsSuite{Name("ok"), nil})
}

func TestWithSuitePanicsBeforeApplication(t *testing.T) {
	// The nil check runs inside WithSuite itself, not when the returned
	// Option is applied: broken wiring fails at the offending line.
	called := false
	func() {
		defer func() { _ = recover() }()
		WithSuite(optionsSuite{func(*options) { called = true }, nil})
	}()
	if called {
		t.Fatal("WithSuite applied options while validating; it must only validate")
	}
}

func TestWithSuiteReadsOptionsOnce(t *testing.T) {
	calls := 0
	s := suiteFunc(func() []Option {
		calls++
		return []Option{Name("once")}
	})
	opt := WithSuite(s)
	o := &options{}
	opt(o)
	opt(o)
	if calls != 1 {
		t.Errorf("Options() called %d times, want 1", calls)
	}
}

// suiteFunc adapts a function to the Suite interface for tests.
type suiteFunc func() []Option

func (f suiteFunc) Options() []Option { return f() }
