package wrr

import (
	"context"
	"fmt"
	"testing"

	"github.com/sylphylabs/forge/selector"
)

func benchmarkWRRNodes(count, offset int) []selector.WeightedNode {
	nodes := make([]selector.WeightedNode, count)
	for i := range count {
		nodes[i] = newMockWeightedNode(fmt.Sprintf("node-%d", offset+i), float64(1+i%10))
	}
	return nodes
}

func BenchmarkPickWorkloads(b *testing.B) {
	ctx := context.Background()
	for _, count := range []int{1, 5, 10, 100} {
		b.Run(fmt.Sprintf("stable/%d", count), func(b *testing.B) {
			balancer := newBalancer()
			nodes := benchmarkWRRNodes(count, 0)
			_, _, _ = balancer.Pick(ctx, nodes)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_, _, _ = balancer.Pick(ctx, nodes)
			}
		})

		b.Run(fmt.Sprintf("add-only/%d", count), func(b *testing.B) {
			nodes := benchmarkWRRNodes(count, 0)
			b.ReportAllocs()
			for b.Loop() {
				balancer := newBalancer()
				for size := 1; size <= count; size++ {
					_, _, _ = balancer.Pick(ctx, nodes[:size])
				}
			}
		})

		b.Run(fmt.Sprintf("removal/%d", count), func(b *testing.B) {
			full := benchmarkWRRNodes(count, 0)
			reduced := full[:max(1, count/2)]
			b.ReportAllocs()
			for b.Loop() {
				balancer := newBalancer()
				_, _, _ = balancer.Pick(ctx, full)
				_, _, _ = balancer.Pick(ctx, reduced)
			}
		})

		b.Run(fmt.Sprintf("replacement/%d", count), func(b *testing.B) {
			first := benchmarkWRRNodes(count, 0)
			second := benchmarkWRRNodes(count, count)
			b.ReportAllocs()
			for b.Loop() {
				balancer := newBalancer()
				_, _, _ = balancer.Pick(ctx, first)
				_, _, _ = balancer.Pick(ctx, second)
			}
		})
	}
}

func BenchmarkPickParallel(b *testing.B) {
	balancer := newBalancer()
	nodes := benchmarkWRRNodes(10, 0)
	ctx := context.Background()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _, _ = balancer.Pick(ctx, nodes)
		}
	})
}
