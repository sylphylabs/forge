package discovery

import (
	"context"
	"net/url"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/serviceconfig"

	"github.com/sylphylabs/forge/registry"
)

func TestWithInsecure(t *testing.T) {
	b := &builder{}
	WithInsecure(true)(b)
	if !b.insecure {
		t.Errorf("expected insecure to be true")
	}
}

func TestWithTimeout(t *testing.T) {
	o := &builder{}
	v := time.Duration(123)
	WithTimeout(v)(o)
	if !reflect.DeepEqual(v, o.timeout) {
		t.Errorf("expected %v, got %v", v, o.timeout)
	}
}

type mockDiscovery struct{}

func (m *mockDiscovery) Instances(_ context.Context, _ string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (m *mockDiscovery) Watch(_ context.Context, _ string) (registry.Watcher, error) {
	time.Sleep(time.Microsecond * 500)
	return &testWatch{}, nil
}

func TestBuilder_Scheme(t *testing.T) {
	b := NewBuilder(&mockDiscovery{})
	if !reflect.DeepEqual("discovery", b.Scheme()) {
		t.Errorf("expected %v, got %v", "discovery", b.Scheme())
	}
}

type mockConn struct{}

func (m *mockConn) UpdateState(resolver.State) error {
	return nil
}

func (m *mockConn) ReportError(error) {}

func (m *mockConn) NewAddress(_ []resolver.Address) {}

func (m *mockConn) NewServiceConfig(_ string) {}

func (m *mockConn) ParseServiceConfig(_ string) *serviceconfig.ParseResult {
	return nil
}

func TestBuilder_Build(t *testing.T) {
	b := NewBuilder(&mockDiscovery{})
	_, err := b.Build(
		resolver.Target{
			URL: url.URL{
				Scheme: resolver.GetDefaultScheme(),
				Path:   "grpc://authority/endpoint",
			},
		},
		&mockConn{},
		resolver.BuildOptions{},
	)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
		return
	}
	timeoutBuilder := NewBuilder(&mockDiscovery{}, WithTimeout(0))
	_, err = timeoutBuilder.Build(
		resolver.Target{
			URL: url.URL{
				Scheme: resolver.GetDefaultScheme(),
				Path:   "grpc://authority/endpoint",
			},
		},
		&mockConn{},
		resolver.BuildOptions{},
	)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

// slowDiscovery blocks Watch until released, then hands out a watcher that
// records whether it was stopped.
type slowDiscovery struct {
	release chan struct{}
	watcher *stopRecordingWatcher
}

func (*slowDiscovery) Instances(_ context.Context, _ string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (d *slowDiscovery) Watch(_ context.Context, _ string) (registry.Watcher, error) {
	<-d.release
	return d.watcher, nil
}

type stopRecordingWatcher struct {
	stopped chan struct{}
}

func (w *stopRecordingWatcher) Next(ctx context.Context) ([]*registry.ServiceInstance, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (w *stopRecordingWatcher) Stop() error {
	close(w.stopped)
	return nil
}

// TestBuilder_BuildTimeoutStopsLateWatcher proves a watcher that materializes
// after Build already timed out does not leak: nobody will use it, so Build's
// cleanup must stop it.
func TestBuilder_BuildTimeoutStopsLateWatcher(t *testing.T) {
	d := &slowDiscovery{
		release: make(chan struct{}),
		watcher: &stopRecordingWatcher{stopped: make(chan struct{})},
	}
	b := NewBuilder(d, WithTimeout(10*time.Millisecond))
	_, err := b.Build(
		resolver.Target{
			URL: url.URL{
				Scheme: resolver.GetDefaultScheme(),
				Path:   "grpc://authority/endpoint",
			},
		},
		&mockConn{},
		resolver.BuildOptions{},
	)
	if err != ErrWatcherCreateTimeout {
		t.Fatalf("expected %v, got %v", ErrWatcherCreateTimeout, err)
	}

	// Watch succeeds only now, after the deadline already passed.
	close(d.release)
	select {
	case <-d.watcher.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("late watcher was never stopped")
	}
}
