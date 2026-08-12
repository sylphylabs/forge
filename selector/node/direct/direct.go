// Package direct wraps nodes with their statically declared weight: the
// weight discovery published is the weight the balancer sees, with no
// runtime feedback.
package direct

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/sylphylabs/forge/selector"
)

// defaultWeight is the effective weight of a node whose publisher declared
// none.
const defaultWeight = 100

var (
	_ selector.WeightedNode        = (*Node)(nil)
	_ selector.WeightedNodeBuilder = (*Builder)(nil)
)

// Node is a weighted node whose weight is fixed at what the service
// publisher declared.
type Node struct {
	node *selector.Node

	// lastPick is the Unix-nano timestamp of the most recent pick.
	lastPick atomic.Int64
}

// Builder wraps nodes as direct nodes.
type Builder struct{}

// Build wraps n.
func (*Builder) Build(n *selector.Node) selector.WeightedNode {
	return &Node{node: n}
}

// Pick records the pick time. Completion carries no feedback for a static
// weight, so the returned DoneFunc is a no-op beyond satisfying the
// exactly-once contract.
func (n *Node) Pick() selector.DoneFunc {
	now := time.Now().UnixNano()
	n.lastPick.Store(now)
	return func(context.Context, selector.DoneInfo) {}
}

// Weight is the declared initial weight, or defaultWeight when none was
// declared.
func (n *Node) Weight() float64 {
	if n.node.InitialWeight > 0 {
		return float64(n.node.InitialWeight)
	}
	return defaultWeight
}

// PickElapsed is the time since the most recent pick.
func (n *Node) PickElapsed() time.Duration {
	return time.Duration(time.Now().UnixNano() - n.lastPick.Load())
}

// Raw returns the underlying node.
func (n *Node) Raw() *selector.Node {
	return n.node
}
