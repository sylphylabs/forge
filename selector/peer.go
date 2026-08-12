package selector

import (
	"context"
)

type peerKey struct{}

// Peer carries the node an RPC was routed to, letting the caller that
// installed it observe which peer served the request.
type Peer struct {
	// Node is the picked node.
	Node *Node
}

// NewPeerContext creates a new context with peer information attached.
func NewPeerContext(ctx context.Context, p *Peer) context.Context {
	return context.WithValue(ctx, peerKey{}, p)
}

// FromPeerContext returns the peer information in ctx if it exists.
func FromPeerContext(ctx context.Context) (p *Peer, ok bool) {
	p, ok = ctx.Value(peerKey{}).(*Peer)
	return
}
