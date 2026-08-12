package selector

import (
	"context"
	"sync/atomic"

	"github.com/sylphylabs/forge/errors"
)

var (
	_ Selector = (*Composite)(nil)
	_ Builder  = (*CompositeBuilder)(nil)
)

// Composite is a [Selector] assembled from the two strategies every selector
// in this module shares: a [WeightedNodeBuilder] that decides what per-node
// state to track, and a [Balancer] that picks among the tracked nodes.
//
// The zero value is not usable; construct with [NewComposite].
type Composite struct {
	nodeBuilder WeightedNodeBuilder
	balancer    Balancer

	nodes atomic.Value // []WeightedNode
}

// NewComposite returns a Composite that wraps applied nodes with nodeBuilder
// and picks among them with balancer.
//
// It panics when either strategy is nil: a selector is assembled while an
// application is wired, and there is no meaningful degraded behavior for a
// selector that cannot weigh or cannot pick.
func NewComposite(nodeBuilder WeightedNodeBuilder, balancer Balancer) *Composite {
	if nodeBuilder == nil {
		panic("selector: NewComposite called with a nil WeightedNodeBuilder")
	}
	if balancer == nil {
		panic("selector: NewComposite called with a nil Balancer")
	}
	return &Composite{nodeBuilder: nodeBuilder, balancer: balancer}
}

// Select picks one node from the current set, after applying any filters
// given as options.
func (c *Composite) Select(ctx context.Context, opts ...SelectOption) (selected *Node, done DoneFunc, err error) {
	var (
		options    selectOptions
		candidates []WeightedNode
	)
	nodes, ok := c.nodes.Load().([]WeightedNode)
	if !ok {
		return nil, nil, ErrNoAvailable
	}
	for _, o := range opts {
		o(&options)
	}
	if len(options.filters) > 0 {
		filtered := make([]*Node, len(nodes))
		for i, wn := range nodes {
			filtered[i] = wn.Raw()
		}
		for _, filter := range options.filters {
			filtered = filter(ctx, filtered)
		}
		// Filters usually subset the applied nodes, whose weighted state must
		// be kept, so match survivors back by identity; a node a filter
		// introduced is wrapped fresh.
		byRaw := make(map[*Node]WeightedNode, len(nodes))
		for _, wn := range nodes {
			byRaw[wn.Raw()] = wn
		}
		candidates = make([]WeightedNode, 0, len(filtered))
		for _, n := range filtered {
			if n == nil {
				continue
			}
			if wn, kept := byRaw[n]; kept {
				candidates = append(candidates, wn)
				continue
			}
			candidates = append(candidates, c.nodeBuilder.Build(n))
		}
	} else {
		candidates = nodes
	}

	if len(candidates) == 0 {
		return nil, nil, ErrNoAvailable
	}
	wn, done, err := c.balancer.Pick(ctx, candidates)
	if err != nil {
		return nil, nil, err
	}
	if done == nil {
		// A nil done would make the caller's exactly-once completion
		// contract unsatisfiable and silently corrupt node statistics, so a
		// balancer that breaks its contract is surfaced here instead.
		return nil, nil, errors.Of(errors.KindInternal).
			Msgf("selector: balancer %T returned a nil DoneFunc", c.balancer)
	}
	if p, ok := FromPeerContext(ctx); ok {
		p.Node = wn.Raw()
	}
	return wn.Raw(), done, nil
}

// Apply installs nodes as the complete current set.
func (c *Composite) Apply(nodes []*Node) {
	weightedNodes := make([]WeightedNode, 0, len(nodes))
	for _, n := range nodes {
		weightedNodes = append(weightedNodes, c.nodeBuilder.Build(n))
	}
	c.nodes.Store(weightedNodes)
}

// CompositeBuilder builds a [Composite] per use site from a node-wrapping
// strategy and a balancer strategy.
//
// The zero value is not usable; construct with [NewCompositeBuilder].
type CompositeBuilder struct {
	node     WeightedNodeBuilder
	balancer BalancerBuilder
}

// NewCompositeBuilder returns a builder whose Build assembles a [Composite]
// from node and balancer. It panics when either strategy is nil, for the
// same reason [NewComposite] does.
func NewCompositeBuilder(node WeightedNodeBuilder, balancer BalancerBuilder) *CompositeBuilder {
	if node == nil {
		panic("selector: NewCompositeBuilder called with a nil WeightedNodeBuilder")
	}
	if balancer == nil {
		panic("selector: NewCompositeBuilder called with a nil BalancerBuilder")
	}
	return &CompositeBuilder{node: node, balancer: balancer}
}

// Build assembles a new Composite with a fresh balancer.
func (b *CompositeBuilder) Build() Selector {
	return NewComposite(b.node, b.balancer.Build())
}
