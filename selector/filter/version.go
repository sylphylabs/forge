// Package filter provides ready-made node filters for [selector.Selector]
// Select calls.
package filter

import (
	"context"

	"github.com/sylphylabs/forge/selector"
)

// Version keeps only the nodes registered with exactly the given version.
func Version(version string) selector.NodeFilter {
	return func(_ context.Context, nodes []*selector.Node) []*selector.Node {
		newNodes := make([]*selector.Node, 0, len(nodes))
		for _, n := range nodes {
			if n.Version == version {
				newNodes = append(newNodes, n)
			}
		}
		return newNodes
	}
}
