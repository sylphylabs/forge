package selector

import (
	"strconv"

	"github.com/sylphylabs/forge/registry"
)

// NewNode projects one endpoint of a discovered service instance onto a
// [Node]. A "weight" entry in the instance metadata, when present and
// numeric, becomes the node's InitialWeight.
func NewNode(scheme, addr string, ins *registry.ServiceInstance) *Node {
	n := &Node{
		Scheme:  scheme,
		Address: addr,
	}
	if ins != nil {
		n.ServiceName = ins.Name
		n.Version = ins.Version
		n.Metadata = ins.Metadata
		if str, ok := ins.Metadata["weight"]; ok {
			if weight, err := strconv.ParseInt(str, 10, 64); err == nil {
				n.InitialWeight = weight
			}
		}
	}
	return n
}
