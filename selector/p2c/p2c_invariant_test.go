package p2c

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/openkratos/kratos/selector"
	"github.com/openkratos/kratos/selector/node/ewma"
)

func p2cWeightedNodes(count int) []selector.WeightedNode {
	builder := &ewma.Builder{}
	nodes := make([]selector.WeightedNode, count)
	for i := range count {
		raw := selector.NewNode("http", fmt.Sprintf("node-%d", i), nil)
		nodes[i] = builder.Build(raw)
	}
	return nodes
}

func TestPrePickDistinctAndUniform(t *testing.T) {
	const (
		nodeCount  = 8
		iterations = 400_000
		tolerance  = 0.02
	)
	nodes := p2cWeightedNodes(nodeCount)
	indices := make(map[selector.WeightedNode]int, nodeCount)
	for i, node := range nodes {
		indices[node] = i
	}

	balancer := &Balancer{}
	counts := make([]int, nodeCount)
	for range iterations {
		a, b := balancer.prePick(nodes)
		if a == b {
			t.Fatalf("prePick returned the same node %q twice", a.Address())
		}
		counts[indices[a]]++
		counts[indices[b]]++
	}

	expected := float64(iterations*2) / nodeCount
	for i, count := range counts {
		deviation := abs(float64(count)-expected) / expected
		if deviation > tolerance {
			t.Errorf("node %d selected %d times; deviation %.2f%% exceeds %.2f%%", i, count, deviation*100, tolerance*100)
		}
	}
}

func TestPrePickConcurrent(t *testing.T) {
	nodes := p2cWeightedNodes(10)
	balancer := &Balancer{}
	var duplicate atomic.Bool
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 10_000 {
				a, b := balancer.prePick(nodes)
				if a == b {
					duplicate.Store(true)
				}
			}
		})
	}
	wg.Wait()
	if duplicate.Load() {
		t.Fatal("concurrent prePick returned duplicate nodes")
	}
}

func abs(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
