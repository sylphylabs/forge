package selector

import (
	"context"
	"time"
)

// Balancer picks one node from a candidate set. Implementations must be safe
// for concurrent use.
type Balancer interface {
	// Pick selects a node. When err is nil, selected and done are both
	// non-nil; done is the node's completion callback from
	// [WeightedNode.Pick].
	Pick(ctx context.Context, nodes []WeightedNode) (selected WeightedNode, done DoneFunc, err error)
}

// BalancerBuilder creates a Balancer per selector, so pick state is never
// shared across selectors.
type BalancerBuilder interface {
	Build() Balancer
}

// WeightedNode is a [Node] with the runtime state one balancing strategy
// tracks for it. It is an interface because the in-tree strategies genuinely
// diverge: a direct node reports its static weight, while an EWMA node folds
// live latency, error, and in-flight statistics into every Weight call.
type WeightedNode interface {
	// Raw returns the underlying node.
	Raw() *Node

	// Weight is the current effective weight.
	Weight() float64

	// Pick records a pick and returns the completion callback the caller
	// must invoke exactly once — see [DoneFunc].
	Pick() DoneFunc

	// PickElapsed is the time since the most recent pick.
	PickElapsed() time.Duration
}

// WeightedNodeBuilder wraps a plain node in one strategy's weighted state.
type WeightedNodeBuilder interface {
	Build(*Node) WeightedNode
}
