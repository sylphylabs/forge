package selector

import (
	"context"
	"errors"
	"math/rand/v2"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylphylabs/forge/registry"
)

var errNodeNotMatch = errors.New("node is not match")

type mockWeightedNode struct {
	node *Node

	lastPick atomic.Int64
}

func (n *mockWeightedNode) Raw() *Node {
	return n.node
}

func (n *mockWeightedNode) Weight() float64 {
	if n.node.InitialWeight > 0 {
		return float64(n.node.InitialWeight)
	}
	return 100
}

func (n *mockWeightedNode) Pick() DoneFunc {
	n.lastPick.Store(time.Now().UnixNano())
	return func(context.Context, DoneInfo) {}
}

func (n *mockWeightedNode) PickElapsed() time.Duration {
	return time.Duration(time.Now().UnixNano() - n.lastPick.Load())
}

type mockWeightedNodeBuilder struct{}

func (b *mockWeightedNodeBuilder) Build(n *Node) WeightedNode {
	return &mockWeightedNode{node: n}
}

func mockFilter(version string) NodeFilter {
	return func(_ context.Context, nodes []*Node) []*Node {
		newNodes := nodes[:0]
		for _, n := range nodes {
			if n.Version == version {
				newNodes = append(newNodes, n)
			}
		}
		return newNodes
	}
}

type mockBalancerBuilder struct{}

func (b *mockBalancerBuilder) Build() Balancer {
	return &mockBalancer{}
}

type mockBalancer struct{}

func (b *mockBalancer) Pick(_ context.Context, nodes []WeightedNode) (selected WeightedNode, done DoneFunc, err error) {
	if len(nodes) == 0 {
		err = ErrNoAvailable
		return
	}
	cur := rand.IntN(len(nodes))
	selected = nodes[cur]
	done = selected.Pick()
	return
}

type mockMustErrorBalancerBuilder struct{}

func (b *mockMustErrorBalancerBuilder) Build() Balancer {
	return &mockMustErrorBalancer{}
}

type mockMustErrorBalancer struct{}

func (b *mockMustErrorBalancer) Pick(_ context.Context, _ []WeightedNode) (selected WeightedNode, done DoneFunc, err error) {
	return nil, nil, errNodeNotMatch
}

// nilDoneBalancer breaks the Balancer contract by returning a node with a
// nil DoneFunc.
type nilDoneBalancer struct{}

func (b *nilDoneBalancer) Build() Balancer { return b }

func (b *nilDoneBalancer) Pick(_ context.Context, nodes []WeightedNode) (WeightedNode, DoneFunc, error) {
	return nodes[0], nil, nil
}

func testNodes() []*Node {
	return []*Node{
		NewNode(
			"http",
			"127.0.0.1:8080",
			&registry.ServiceInstance{
				ID:        "127.0.0.1:8080",
				Name:      "helloworld",
				Version:   "v2.0.0",
				Endpoints: []string{"http://127.0.0.1:8080"},
				Metadata:  map[string]string{"weight": "10"},
			}),
		NewNode(
			"http",
			"127.0.0.1:9090",
			&registry.ServiceInstance{
				ID:        "127.0.0.1:9090",
				Name:      "helloworld",
				Version:   "v1.0.0",
				Endpoints: []string{"http://127.0.0.1:9090"},
				Metadata:  map[string]string{"weight": "10"},
			}),
	}
}

func TestComposite(t *testing.T) {
	builder := NewCompositeBuilder(&mockWeightedNodeBuilder{}, &mockBalancerBuilder{})
	selector := builder.Build()
	nodes := testNodes()

	selector.Apply(nodes)
	n, done, err := selector.Select(context.Background(), WithNodeFilter(mockFilter("v2.0.0")))
	if err != nil {
		t.Errorf("expect %v, got %v", nil, err)
	}
	if n == nil {
		t.Fatalf("expect a node, got nil")
	}
	if done == nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if !reflect.DeepEqual("v2.0.0", n.Version) {
		t.Errorf("expect %v, got %v", "v2.0.0", n.Version)
	}
	if n.Scheme == "" {
		t.Errorf("expect %v, got %v", "", n.Scheme)
	}
	if n.Address == "" {
		t.Errorf("expect %v, got %v", "", n.Address)
	}
	if n.InitialWeight != 10 {
		t.Errorf("expect %v, got %v", 10, n.InitialWeight)
	}
	if n.Metadata == nil {
		t.Errorf("expect %v, got %v", nil, n.Metadata)
	}
	if !reflect.DeepEqual("helloworld", n.ServiceName) {
		t.Errorf("expect %v, got %v", "helloworld", n.ServiceName)
	}
	done(context.Background(), DoneInfo{})

	// A selected node must preserve identity with an applied node.
	if n != nodes[0] {
		t.Errorf("selected node %p is not the applied node %p", n, nodes[0])
	}

	// peer in ctx
	ctx := NewPeerContext(context.Background(), &Peer{})
	n, done, err = selector.Select(ctx)
	if err != nil {
		t.Errorf("expect %v, got %v", nil, err)
	}
	if done == nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n == nil {
		t.Errorf("expect %v, got %v", nil, n)
	}
	if p, ok := FromPeerContext(ctx); !ok || p.Node != n {
		t.Errorf("peer node = %v, want the selected node %v", p.Node, n)
	}

	// no v3.0.0 instance
	n, done, err = selector.Select(context.Background(), WithNodeFilter(mockFilter("v3.0.0")))
	if !errors.Is(ErrNoAvailable, err) {
		t.Errorf("expect %v, got %v", ErrNoAvailable, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}

	// apply zero instance
	selector.Apply([]*Node{})
	n, done, err = selector.Select(context.Background(), WithNodeFilter(mockFilter("v2.0.0")))
	if !errors.Is(ErrNoAvailable, err) {
		t.Errorf("expect %v, got %v", ErrNoAvailable, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}

	// apply nil
	selector.Apply(nil)
	n, done, err = selector.Select(context.Background(), WithNodeFilter(mockFilter("v2.0.0")))
	if !errors.Is(ErrNoAvailable, err) {
		t.Errorf("expect %v, got %v", ErrNoAvailable, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}

	// without node_filters
	n, done, err = selector.Select(context.Background())
	if !errors.Is(ErrNoAvailable, err) {
		t.Errorf("expect %v, got %v", ErrNoAvailable, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}
}

func TestWithoutApply(t *testing.T) {
	builder := NewCompositeBuilder(&mockWeightedNodeBuilder{}, &mockBalancerBuilder{})
	selector := builder.Build()
	n, done, err := selector.Select(context.Background())
	if !errors.Is(ErrNoAvailable, err) {
		t.Errorf("expect %v, got %v", ErrNoAvailable, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}
}

// A filter may substitute nodes of its own; the composite must wrap such a
// node instead of dropping it.
func TestCompositeFilterReturnsForeignNode(t *testing.T) {
	builder := NewCompositeBuilder(&mockWeightedNodeBuilder{}, &mockBalancerBuilder{})
	selector := builder.Build()
	selector.Apply([]*Node{NewNode("http", "127.0.0.1:8080", nil)})

	want := NewNode("http", "127.0.0.1:9090", nil)
	node, done, err := selector.Select(t.Context(), WithNodeFilter(func(context.Context, []*Node) []*Node {
		return []*Node{nil, want}
	}))
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if node != want {
		t.Fatalf("Select() node = %v, want %v", node, want)
	}
	if done == nil {
		t.Fatal("Select() done is nil")
	}
}

// A filter that keeps applied nodes must not reset their weighted state: the
// composite has to hand the balancer the same WeightedNode it applied.
func TestCompositeFilterPreservesWeightedState(t *testing.T) {
	var builds atomic.Int64
	counting := builderFunc(func(n *Node) WeightedNode {
		builds.Add(1)
		return &mockWeightedNode{node: n}
	})
	selector := NewComposite(counting, &mockBalancer{})
	selector.Apply(testNodes())
	applied := builds.Load()

	if _, _, err := selector.Select(t.Context(), WithNodeFilter(func(_ context.Context, nodes []*Node) []*Node {
		return nodes
	})); err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if got := builds.Load(); got != applied {
		t.Fatalf("filter round trip rebuilt weighted nodes: %d builds, want %d", got, applied)
	}
}

type builderFunc func(*Node) WeightedNode

func (f builderFunc) Build(n *Node) WeightedNode { return f(n) }

func TestNoPick(t *testing.T) {
	builder := NewCompositeBuilder(&mockWeightedNodeBuilder{}, &mockMustErrorBalancerBuilder{})
	selector := builder.Build()
	selector.Apply(testNodes())
	n, done, err := selector.Select(context.Background())
	if !errors.Is(errNodeNotMatch, err) {
		t.Errorf("expect %v, got %v", errNodeNotMatch, err)
	}
	if done != nil {
		t.Errorf("expect %v, got %v", nil, done)
	}
	if n != nil {
		t.Errorf("expect %v, got %v", nil, n)
	}
}

// A balancer returning a nil DoneFunc breaks its contract; the composite
// must surface an error instead of propagating the nil.
func TestNilDonePickIsRejected(t *testing.T) {
	selector := NewComposite(&mockWeightedNodeBuilder{}, &nilDoneBalancer{})
	selector.Apply(testNodes())
	n, done, err := selector.Select(context.Background())
	if err == nil {
		t.Fatal("expect an error for a nil DoneFunc, got nil")
	}
	if done != nil {
		t.Errorf("expect nil done, got %v", done)
	}
	if n != nil {
		t.Errorf("expect nil node, got %v", n)
	}
}

func TestCompositeBuilderBuild(t *testing.T) {
	builder := NewCompositeBuilder(&mockWeightedNodeBuilder{}, &mockBalancerBuilder{})
	if builder.Build() == nil {
		t.Error("expect a selector, got nil")
	}
}

func TestNewCompositeNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewComposite(nil, nil) did not panic")
		}
	}()
	NewComposite(nil, nil)
}

func TestWithNodeFilterAccumulates(t *testing.T) {
	var opts selectOptions
	first := func(_ context.Context, nodes []*Node) []*Node { return nodes }
	second := func(_ context.Context, _ []*Node) []*Node { return nil }
	WithNodeFilter(first)(&opts)
	WithNodeFilter(second)(&opts)
	if len(opts.filters) != 2 {
		t.Fatalf("len(filters) = %d, want 2", len(opts.filters))
	}
	nodes := []*Node{NewNode("http", "127.0.0.1:8000", nil)}
	if got := opts.filters[0](context.Background(), nodes); len(got) != 1 {
		t.Fatalf("first filter returned %d nodes, want 1", len(got))
	}
	if got := opts.filters[1](context.Background(), nodes); got != nil {
		t.Fatalf("second filter returned %v, want nil", got)
	}
}
