// Package p2c provides a "power of two choices" selector: each pick compares
// two random nodes by their EWMA-tracked latency and health and takes the
// better one, which balances load with O(1) work per pick.
package p2c

import (
	"context"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/node/ewma"
)

// forcePick bounds how long a node can go unpicked: a node idle longer is
// picked once regardless of weight, so its statistics keep refreshing and a
// recovered node is rediscovered.
const forcePick = time.Second * 3

// Name is the balancer name, "p2c".
const Name = "p2c"

var (
	_ selector.Balancer        = (*balancer)(nil)
	_ selector.BalancerBuilder = (*Builder)(nil)
)

// New returns a p2c selector.
func New() *selector.Composite {
	return selector.NewComposite(&ewma.Builder{}, &balancer{})
}

// balancer picks the better of two random choices by EWMA weight.
type balancer struct {
	picked atomic.Bool
}

// prePick chooses two distinct nodes uniformly at random.
func (s *balancer) prePick(nodes []selector.WeightedNode) (nodeA selector.WeightedNode, nodeB selector.WeightedNode) {
	// rand/v2 top-level functions are safe for concurrent use, so no per-balancer
	// mutex or Rand is needed and picks no longer serialize on local random state.
	a := rand.IntN(len(nodes))
	b := rand.IntN(len(nodes) - 1)
	if b >= a {
		b = b + 1
	}
	nodeA, nodeB = nodes[a], nodes[b]
	return
}

// Pick picks a node.
func (s *balancer) Pick(_ context.Context, nodes []selector.WeightedNode) (selector.WeightedNode, selector.DoneFunc, error) {
	if len(nodes) == 0 {
		return nil, nil, selector.ErrNoAvailable
	}
	if len(nodes) == 1 {
		done := nodes[0].Pick()
		return nodes[0], done, nil
	}

	var pc, upc selector.WeightedNode
	nodeA, nodeB := s.prePick(nodes)
	// nodeB.Weight() reflects both the published weight and live feedback.
	if nodeB.Weight() > nodeA.Weight() {
		pc, upc = nodeB, nodeA
	} else {
		pc, upc = nodeA, nodeB
	}

	// If the losing node has not been picked within forcePick, pick it once
	// to refresh its success rate and latency statistics. The CAS admits one
	// forced pick at a time so a stampede cannot flood a struggling node.
	if upc.PickElapsed() > forcePick && s.picked.CompareAndSwap(false, true) {
		defer s.picked.Store(false)
		pc = upc
	}
	done := pc.Pick()
	return pc, done, nil
}

// NewBuilder returns a builder for p2c selectors.
func NewBuilder() *selector.CompositeBuilder {
	return selector.NewCompositeBuilder(&ewma.Builder{}, &Builder{})
}

// Builder builds p2c balancers.
type Builder struct{}

// Build returns a new balancer with fresh pick state.
func (b *Builder) Build() selector.Balancer {
	return &balancer{}
}
