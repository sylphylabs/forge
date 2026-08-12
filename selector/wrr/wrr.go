// Package wrr provides a weighted round robin selector: nodes are picked in
// proportion to their declared weights, with no runtime feedback.
package wrr

import (
	"context"
	"sync"

	"github.com/sylphylabs/forge/selector"
	"github.com/sylphylabs/forge/selector/node/direct"
)

// Name is the balancer name, "wrr".
const Name = "wrr"

var (
	_ selector.Balancer        = (*balancer)(nil)
	_ selector.BalancerBuilder = (*Builder)(nil)
)

// balancer implements smooth weighted round robin over the interleaving
// algorithm nginx uses: each pick advances every node's current weight by
// its effective weight and selects the leader, then sets the leader back by
// the total, so picks interleave instead of bursting per node.
type balancer struct {
	mu            sync.Mutex
	currentWeight map[string]float64
}

func newBalancer() *balancer {
	return &balancer{currentWeight: make(map[string]float64)}
}

// New returns a weighted round robin selector.
func New() *selector.Composite {
	return selector.NewComposite(&direct.Builder{}, newBalancer())
}

// Pick picks a node by smooth weighted round robin.
func (p *balancer) Pick(_ context.Context, nodes []selector.WeightedNode) (selector.WeightedNode, selector.DoneFunc, error) {
	if len(nodes) == 0 {
		return nil, nil, selector.ErrNoAvailable
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	var totalWeight float64
	var selected selector.WeightedNode
	var selectWeight float64

	for _, node := range nodes {
		totalWeight += node.Weight()
		cwt := p.currentWeight[node.Raw().Address]
		// current += effectiveWeight
		cwt += node.Weight()
		p.currentWeight[node.Raw().Address] = cwt
		if selected == nil || selectWeight < cwt {
			selectWeight = cwt
			selected = node
		}
	}
	p.currentWeight[selected.Raw().Address] = selectWeight - totalWeight

	// After the loop, currentWeight has an entry for every current node, plus any
	// leftover entries for nodes that have disappeared from service discovery. So
	// len(currentWeight) > len(nodes) exactly when stale entries exist: drop them
	// to keep the map from growing without bound as nodes churn. When the node set
	// is unchanged (the common case) the sizes match and cleanup is skipped, so the
	// per-pick cost is just the algorithm itself with no extra bookkeeping.
	if len(p.currentWeight) > len(nodes) {
		p.cleanupStaleEntries(nodes)
	}

	d := selected.Pick()
	return selected, d, nil
}

// cleanupStaleEntries removes currentWeight entries whose node is no longer present.
func (p *balancer) cleanupStaleEntries(nodes []selector.WeightedNode) {
	current := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		current[node.Raw().Address] = struct{}{}
	}
	for address := range p.currentWeight {
		if _, ok := current[address]; !ok {
			delete(p.currentWeight, address)
		}
	}
}

// NewBuilder returns a builder for weighted round robin selectors.
func NewBuilder() *selector.CompositeBuilder {
	return selector.NewCompositeBuilder(&direct.Builder{}, &Builder{})
}

// Builder builds wrr balancers.
type Builder struct{}

// Build returns a new balancer with fresh round robin state.
func (b *Builder) Build() selector.Balancer {
	return newBalancer()
}
