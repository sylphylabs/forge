package grpc

import (
	"context"
	"reflect"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"

	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/wrr"
	"github.com/sylphylabs/forge/transport/grpc/resolver/discovery"
)

type testSubConn struct{ balancer.SubConn }

func TestBalancerBuildUsesDefaultSelector(t *testing.T) {
	conn := testSubConn{}
	picker := (&balancerBuilder{}).Build(base.PickerBuildInfo{
		ReadySCs: map[balancer.SubConn]base.SubConnInfo{
			conn: {Address: resolver.Address{Addr: "127.0.0.1:9000"}},
		},
	})

	result, err := picker.Pick(balancer.PickInfo{Ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if result.SubConn != conn {
		t.Fatalf("picked SubConn %v, want %v", result.SubConn, conn)
	}
	result.Done(balancer.DoneInfo{})
}

// countingBuilder records how many selectors it built, so a test can tell
// whether the balancer consulted the policy the client configured. The count is
// atomic because gRPC builds a picker on its own balancer goroutine.
type countingBuilder struct {
	built atomic.Int64
	inner selector.Builder
}

func (b *countingBuilder) Build() selector.Selector {
	b.built.Add(1)
	return b.inner.Build()
}

// Built reports how many selectors this builder has produced.
func (b *countingBuilder) Built() int64 { return b.built.Load() }

func TestBalancerBuildUsesAddressSelector(t *testing.T) {
	configured := &countingBuilder{inner: wrr.NewBuilder()}
	conn := testSubConn{}
	addr := discovery.NewAddressWithSelectorBuilder(
		resolver.Address{Addr: "127.0.0.1:9000"}, configured)

	picker := (&balancerBuilder{}).Build(base.PickerBuildInfo{
		ReadySCs: map[balancer.SubConn]base.SubConnInfo{conn: {Address: addr}},
	})

	if configured.Built() != 1 {
		t.Fatalf("configured selector built %d times, want 1", configured.Built())
	}
	result, err := picker.Pick(balancer.PickInfo{Ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if result.SubConn != conn {
		t.Fatalf("picked SubConn %v, want %v", result.SubConn, conn)
	}
}

// TestBalancerBuildSelectorsAreIndependent proves two clients in one process
// balance with their own policy rather than a shared one.
func TestBalancerBuildSelectorsAreIndependent(t *testing.T) {
	first := &countingBuilder{inner: wrr.NewBuilder()}
	second := &countingBuilder{inner: wrr.NewBuilder()}

	for _, b := range []*countingBuilder{first, second} {
		addr := discovery.NewAddressWithSelectorBuilder(
			resolver.Address{Addr: "127.0.0.1:9000"}, b)
		(&balancerBuilder{}).Build(base.PickerBuildInfo{
			ReadySCs: map[balancer.SubConn]base.SubConnInfo{testSubConn{}: {Address: addr}},
		})
	}

	if first.Built() != 1 || second.Built() != 1 {
		t.Fatalf("built first=%d second=%d, want 1 each", first.Built(), second.Built())
	}
}

func TestTrailer(t *testing.T) {
	trailer := Trailer(metadata.New(map[string]string{"a": "b"}))
	if !reflect.DeepEqual("b", trailer.Get("a")) {
		t.Errorf("expect %v, got %v", "b", trailer.Get("a"))
	}
	if !reflect.DeepEqual("", trailer.Get("notfound")) {
		t.Errorf("expect %v, got %v", "", trailer.Get("notfound"))
	}
}

func TestFilters(t *testing.T) {
	o := &clientOptions{}

	WithNodeFilter(func(_ context.Context, nodes []selector.Node) []selector.Node {
		return nodes
	})(o)
	if !reflect.DeepEqual(1, len(o.filters)) {
		t.Errorf("expect %v, got %v", 1, len(o.filters))
	}
}
