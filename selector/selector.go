// Package selector picks one service node per request from a set that
// service discovery keeps current.
//
// A selector pairs two strategies: a [WeightedNodeBuilder] that decides what
// per-node state to track (a static weight, or live latency and error
// statistics), and a [Balancer] that picks among the weighted nodes.
// [NewComposite] assembles the two; the wrr, p2c, and random subpackages
// ship ready-made pairings.
package selector

import (
	"context"

	"github.com/sylphylabs/forge/errors"
)

// ErrNoAvailable is returned when no node passed the configured filters.
var ErrNoAvailable = errors.MustDefine(errors.KindUnavailable, errors.Domain, "NO_AVAILABLE_NODE").
	Msg("no available node")

// Selector picks one node per request from the set most recently applied.
type Selector interface {
	Rebalancer

	// Select picks a node. When err is nil, selected and done are both
	// non-nil, and the caller must invoke done exactly once when the
	// request completes — see [DoneFunc].
	Select(ctx context.Context, opts ...SelectOption) (selected *Node, done DoneFunc, err error)
}

// Rebalancer replaces the node set when service discovery reports a change.
type Rebalancer interface {
	// Apply installs nodes as the complete current set.
	Apply(nodes []*Node)
}

// Builder creates a Selector per use site. Transports build one selector per
// client, so stateful balancers start fresh instead of sharing pick state
// across unrelated connections.
type Builder interface {
	Build() Selector
}

// Node describes one service instance a selector can pick. It is plain data
// — the per-endpoint projection of a [github.com/sylphylabs/forge/registry.ServiceInstance]
// — so it is a struct; behavior belongs to [WeightedNode].
//
// Selectors pass nodes by pointer and preserve identity: the *Node returned
// by Select is one of the pointers given to Apply unless a [NodeFilter]
// substituted its own, so a caller may key auxiliary state, such as a
// connection, by pointer.
type Node struct {
	// Scheme is the transport scheme, such as "http" or "grpc".
	Scheme string
	// Address is the endpoint address, unique within one service.
	Address string
	// ServiceName is the registered service name.
	ServiceName string
	// Version is the service version as registered.
	Version string
	// InitialWeight is the scheduling weight the service publisher declared.
	// Zero means none was declared; balancers substitute their own default.
	InitialWeight int64
	// Metadata is the key-value metadata registered with the instance.
	Metadata map[string]string
}

// DoneInfo describes the outcome of the request a pick served.
type DoneInfo struct {
	// Err is the error the request ended with, nil on success.
	Err error
	// ReplyMetadata is the response metadata, such as gRPC trailers.
	ReplyMetadata ReplyMetadata

	// BytesSent reports whether any bytes were sent to the server.
	BytesSent bool
	// BytesReceived reports whether any bytes were received from the server.
	BytesReceived bool
}

// ReplyMetadata is a read-only view of response metadata.
type ReplyMetadata interface {
	Get(key string) string
}

// DoneFunc reports the outcome of the request a pick served.
//
// The caller must invoke it exactly once, when the request completes.
// Weighted nodes adjust their statistics — in-flight counts, latency and
// success averages — assuming one completion per pick: a dropped call leaks
// in-flight accounting and permanently depresses the node's weight, and a
// doubled call corrupts it in the other direction.
type DoneFunc func(ctx context.Context, di DoneInfo)
