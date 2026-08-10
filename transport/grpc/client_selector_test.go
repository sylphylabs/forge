package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	ggrpc "google.golang.org/grpc"

	"github.com/sylphylabs/forge/registry"
	"github.com/sylphylabs/forge/selector/wrr"
)

// staticDiscovery reports one instance at a fixed address and then blocks, the
// minimum a channel needs to reach READY and build a picker.
type staticDiscovery struct{ addr string }

func (*staticDiscovery) GetService(context.Context, string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (d *staticDiscovery) Watch(ctx context.Context, _ string) (registry.Watcher, error) {
	return &staticWatcher{addr: d.addr, ctx: ctx}, nil
}

type staticWatcher struct {
	addr string
	ctx  context.Context
	sent bool
}

func (w *staticWatcher) Next() ([]*registry.ServiceInstance, error) {
	if w.sent {
		<-w.ctx.Done()
		return nil, w.ctx.Err()
	}
	w.sent = true
	return []*registry.ServiceInstance{{
		ID:        "1",
		Name:      "selector-test",
		Endpoints: []string{"grpc://" + w.addr},
	}}, nil
}

func (*staticWatcher) Stop() error { return nil }

// TestClientSelectorReachesPicker dials a real server through discovery and
// proves the builder passed to WithSelector is the one the balancer builds
// from. The unit tests above reach the picker directly; this one covers the
// address-attribute path that carries the policy from NewClient to gRPC.
func TestClientSelectorReachesPicker(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := ggrpc.NewServer()
	go func() { _ = srv.Serve(lis) }()
	defer srv.Stop()

	configured := &countingBuilder{inner: wrr.NewBuilder()}
	conn, err := NewClient(t.Context(),
		WithEndpoint("discovery:///selector-test"),
		WithDiscovery(&staticDiscovery{addr: lis.Addr().String()}),
		WithSelector(configured),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if configured.Built() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the configured selector was never built; the balancer did not use it")
}
