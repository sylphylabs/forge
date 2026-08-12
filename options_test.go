package forge

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/transport"
)

func TestID(t *testing.T) {
	o := &options{}
	v := "123"
	WithID(v)(o)
	if !reflect.DeepEqual(v, o.id) {
		t.Fatalf("o.id:%s is not equal to v:%s", o.id, v)
	}
}

func TestName(t *testing.T) {
	o := &options{}
	v := "abc"
	WithName(v)(o)
	if !reflect.DeepEqual(v, o.name) {
		t.Fatalf("o.name:%s is not equal to v:%s", o.name, v)
	}
}

func TestVersion(t *testing.T) {
	o := &options{}
	v := "123"
	WithVersion(v)(o)
	if !reflect.DeepEqual(v, o.version) {
		t.Fatalf("o.version:%s is not equal to v:%s", o.version, v)
	}
}

func TestMetadata(t *testing.T) {
	o := &options{}
	v := map[string]string{
		"a": "1",
		"b": "2",
	}
	WithMetadata(v)(o)
	if !reflect.DeepEqual(v, o.metadata) {
		t.Fatalf("o.metadata:%s is not equal to v:%s", o.metadata, v)
	}
}

func TestEndpoint(t *testing.T) {
	o := &options{}
	v := []*url.URL{
		{Host: "example.com"},
		{Host: "foo.com"},
	}
	WithEndpoint(v...)(o)
	if !reflect.DeepEqual(v, o.endpoints) {
		t.Fatalf("o.endpoints:%s is not equal to v:%s", o.endpoints, v)
	}
}

func TestContext(t *testing.T) {
	type ctxKey struct {
		Key string
	}
	o := &options{}
	v := context.WithValue(context.TODO(), ctxKey{Key: "context"}, "b")
	WithContext(v)(o)
	if !reflect.DeepEqual(v, o.ctx) {
		t.Fatalf("o.ctx:%s is not equal to v:%s", o.ctx, v)
	}
}

func TestLogger(t *testing.T) {
	o := &options{}
	v := slog.New(slog.NewTextHandler(io.Discard, nil))
	WithLogger(v)(o)
	if !reflect.DeepEqual(v, o.logger) {
		t.Fatalf("o.logger:%v is not equal to v:%v", o.logger, v)
	}
}

type mockServer struct {
	stopFn func(context.Context) error
}

func (m *mockServer) Start(_ context.Context) error { return nil }
func (m *mockServer) Stop(ctx context.Context) error {
	if m.stopFn != nil {
		return m.stopFn(ctx)
	}
	return nil
}

func TestServer(t *testing.T) {
	o := &options{}
	v := []transport.Server{
		&mockServer{}, &mockServer{},
	}
	WithServer(v...)(o)
	if !reflect.DeepEqual(v, o.servers) {
		t.Fatalf("o.servers:%s is not equal to v:%s", o.servers, v)
	}
}

func TestServerAccumulates(t *testing.T) {
	o := &options{}
	first := &mockServer{}
	second := &mockServer{}
	WithServer(first)(o)
	WithServer(second)(o)
	want := []transport.Server{first, second}
	if !reflect.DeepEqual(want, o.servers) {
		t.Fatalf("o.servers = %v, want %v", o.servers, want)
	}
}

func TestEndpointAccumulates(t *testing.T) {
	o := &options{}
	first := &url.URL{Host: "example.com"}
	second := &url.URL{Host: "foo.com"}
	WithEndpoint(first)(o)
	WithEndpoint(second)(o)
	want := []*url.URL{first, second}
	if !reflect.DeepEqual(want, o.endpoints) {
		t.Fatalf("o.endpoints = %v, want %v", o.endpoints, want)
	}
}

func TestSignalAccumulates(t *testing.T) {
	o := &options{}
	first := &mockSignal{}
	second := &mockSignal{}
	WithSignal(first)(o)
	WithSignal(second)(o)
	want := []os.Signal{first, second}
	if !reflect.DeepEqual(want, o.sigs) {
		t.Fatalf("o.sigs = %v, want %v", o.sigs, want)
	}
}

func TestMetadataMerges(t *testing.T) {
	o := &options{}
	WithMetadata(map[string]string{"a": "1", "b": "2"})(o)
	WithMetadata(map[string]string{"b": "overridden", "c": "3"})(o)
	want := map[string]string{"a": "1", "b": "overridden", "c": "3"}
	if !reflect.DeepEqual(want, o.metadata) {
		t.Fatalf("o.metadata = %v, want %v", o.metadata, want)
	}
}

type mockSignal struct{}

func (m *mockSignal) String() string { return "sig" }
func (m *mockSignal) Signal()        {}

func TestSignal(t *testing.T) {
	o := &options{}
	v := []os.Signal{
		&mockSignal{}, &mockSignal{},
	}
	WithSignal(v...)(o)
	if !reflect.DeepEqual(v, o.sigs) {
		t.Fatal("o.sigs is not equal to v")
	}
}

type mockRegistrar struct{}

func (m *mockRegistrar) Register(_ context.Context, _ *registry.ServiceInstance) error {
	return nil
}

func (m *mockRegistrar) Deregister(_ context.Context, _ *registry.ServiceInstance) error {
	return nil
}

func TestRegistrar(t *testing.T) {
	o := &options{}
	v := &mockRegistrar{}
	WithRegistrar(v)(o)
	if !reflect.DeepEqual(v, o.registrar) {
		t.Fatal("o.registrar is not equal to v")
	}
}

func TestRegistrarTimeout(t *testing.T) {
	o := &options{}
	v := time.Duration(123)
	WithRegistrarTimeout(v)(o)
	if !reflect.DeepEqual(v, o.registrarTimeout) {
		t.Fatal("o.registrarTimeout is not equal to v")
	}
}

func TestStopTimeout(t *testing.T) {
	o := &options{}
	v := time.Duration(123)
	WithStopTimeout(v)(o)
	if !reflect.DeepEqual(v, o.stopTimeout) {
		t.Fatal("o.stopTimeout is not equal to v")
	}
}

func TestAfterStopTimeout(t *testing.T) {
	o := &options{}
	v := time.Duration(123)
	WithAfterStopTimeout(v)(o)
	if o.afterStopTimeout != v {
		t.Fatalf("afterStopTimeout = %v, want %v", o.afterStopTimeout, v)
	}
}

func TestBeforeStart(t *testing.T) {
	o := &options{}
	v := func(_ context.Context) error {
		t.Log("BeforeStart...")
		return nil
	}
	WithBeforeStart(v)(o)
}

func TestBeforeStop(t *testing.T) {
	o := &options{}
	v := func(_ context.Context) error {
		t.Log("BeforeStop...")
		return nil
	}
	WithBeforeStop(v)(o)
}

func TestAfterStart(t *testing.T) {
	o := &options{}
	v := func(_ context.Context) error {
		t.Log("AfterStart...")
		return nil
	}
	WithAfterStart(v)(o)
}

func TestAfterStop(t *testing.T) {
	o := &options{}
	v := func(_ context.Context) error {
		t.Log("AfterStop...")
		return nil
	}
	WithAfterStop(v)(o)
}
