package forge

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/sylphylabs/forge/diagnosis"
)

func TestAppProbeReportsIdentity(t *testing.T) {
	endpoint, _ := url.Parse("grpc://127.0.0.1:9000")
	app := New(
		WithID("instance-1"),
		WithName("checkout"),
		WithVersion("v1.2.3"),
		WithMetadata(map[string]string{"region": "eu-west-1"}),
		WithEndpoint(endpoint),
	)

	reg := diagnosis.NewRegistry()
	reg.Register("app", AppProbe(app))

	res, ok := reg.Probe(context.Background(), "app")
	if !ok || res.Err != nil {
		t.Fatalf("app probe = %+v, ok=%v", res, ok)
	}
	snapshot, ok := res.Value.(AppSnapshot)
	if !ok {
		t.Fatalf("value has type %T, want AppSnapshot", res.Value)
	}
	want := AppSnapshot{
		ID:       "instance-1",
		Name:     "checkout",
		Version:  "v1.2.3",
		Metadata: map[string]string{"region": "eu-west-1"},
	}
	if !reflect.DeepEqual(snapshot, want) {
		t.Fatalf("snapshot = %+v, want %+v", snapshot, want)
	}

	// The ProbeFunc contract requires a JSON-serializable value.
	if _, err := json.Marshal(snapshot); err != nil {
		t.Fatalf("AppSnapshot must serialize: %v", err)
	}
}

func TestAppProbeReadsLive(t *testing.T) {
	app := New(WithName("checkout"))
	probe := AppProbe(app)

	res, err := probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(AppSnapshot).Endpoints; len(got) != 0 {
		t.Fatalf("endpoints before start = %v, want none", got)
	}

	// Endpoints resolved after the probe was built must show up: the probe
	// reads the AppInfo when it runs, not when it is wired.
	endpoint, _ := url.Parse("http://127.0.0.1:8000")
	app.mu.Lock()
	app.endpoints = []string{endpoint.String()}
	app.mu.Unlock()

	res, err = probe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := res.(AppSnapshot).Endpoints; len(got) != 1 || got[0] != endpoint.String() {
		t.Fatalf("endpoints after start = %v, want [%s]", got, endpoint)
	}
}

func TestAppProbeNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("AppProbe(nil) must panic")
		}
	}()
	AppProbe(nil)
}

// diagnosticSuite is an integration bundle that carries its own probes: it
// contributes lifecycle hooks that register an application-identity probe
// into the registry the suite was built with. It demonstrates that a Suite
// needs no dedicated extension point to participate in diagnosis — the
// registry is a value the suite closes over, and AfterStart supplies the
// AppInfo through the context.
type diagnosticSuite struct {
	registry *diagnosis.Registry
}

func (s *diagnosticSuite) Options() []Option {
	return []Option{
		WithAfterStart(func(ctx context.Context) error {
			info, ok := FromContext(ctx)
			if !ok {
				return nil
			}
			s.registry.Register("app", AppProbe(info))
			return nil
		}),
	}
}

func TestSuiteRegistersProbes(t *testing.T) {
	reg := diagnosis.NewRegistry()
	app := New(
		WithName("checkout"),
		WithVersion("v9"),
		WithSuite(&diagnosticSuite{registry: reg}),
	)

	done := make(chan error, 1)
	go func() { done <- app.Run() }()

	// The suite's AfterStart hook has run once the probe appears.
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if res, ok := reg.Probe(deadlineCtx, "app"); ok {
			if res.Err != nil {
				t.Fatalf("suite-registered probe failed: %v", res.Err)
			}
			if got := res.Value.(AppSnapshot).Name; got != "checkout" {
				t.Fatalf("probe name = %q, want checkout", got)
			}
			break
		}
		select {
		case <-deadlineCtx.Done():
			t.Fatal("suite never registered its probe")
		case <-time.After(5 * time.Millisecond):
		}
	}

	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
