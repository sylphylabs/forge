package p2c

import (
	"context"
	"fmt"
	"testing"

	"github.com/openkratos/kratos/registry"
	"github.com/openkratos/kratos/selector"
)

func benchSelector(nodeCount int) selector.Selector {
	s := New()
	nodes := make([]selector.Node, 0, nodeCount)
	for i := 0; i < nodeCount; i++ {
		addr := fmt.Sprintf("127.0.0.%d:8080", i)
		nodes = append(nodes, selector.NewNode("http", addr, &registry.ServiceInstance{
			ID:       addr,
			Version:  "v1.0.0",
			Metadata: map[string]string{"weight": "10"},
		}))
	}
	s.Apply(nodes)
	return s
}

// BenchmarkSelectParallel exercises a shared balancer from GOMAXPROCS
// goroutines, matching how clients use a balancer under concurrent load.
func BenchmarkSelectParallel(b *testing.B) {
	s := benchSelector(10)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, done, err := s.Select(ctx)
			if err != nil {
				b.Fatal(err)
			}
			done(ctx, selector.DoneInfo{})
		}
	})
}

// BenchmarkSelectSerial measures the single-goroutine selection cost.
func BenchmarkSelectSerial(b *testing.B) {
	s := benchSelector(10)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, done, err := s.Select(ctx)
		if err != nil {
			b.Fatal(err)
		}
		done(ctx, selector.DoneInfo{})
	}
}
