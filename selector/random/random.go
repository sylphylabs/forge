// Package random provides a uniformly random selector: every pick chooses
// among the candidates with equal probability, ignoring weights.
package random

import (
	"context"
	"math/rand/v2"

	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/node/direct"
)

// Name is the balancer name, "random".
const Name = "random"

var (
	_ selector.Balancer        = (*balancer)(nil)
	_ selector.BalancerBuilder = (*Builder)(nil)
)

// balancer picks uniformly at random.
type balancer struct{}

// New returns a random selector.
func New() *selector.Composite {
	return selector.NewComposite(&direct.Builder{}, balancer{})
}

// Pick picks a node uniformly at random.
func (p balancer) Pick(_ context.Context, nodes []selector.WeightedNode) (selector.WeightedNode, selector.DoneFunc, error) {
	if len(nodes) == 0 {
		return nil, nil, selector.ErrNoAvailable
	}
	cur := rand.IntN(len(nodes))
	selected := nodes[cur]
	d := selected.Pick()
	return selected, d, nil
}

// NewBuilder returns a builder for random selectors.
func NewBuilder() *selector.CompositeBuilder {
	return selector.NewCompositeBuilder(&direct.Builder{}, &Builder{})
}

// Builder builds random balancers.
type Builder struct{}

// Build returns a new balancer.
func (b *Builder) Build() selector.Balancer {
	return balancer{}
}
