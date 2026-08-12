package selector

import "context"

// NodeFilter narrows the candidate set of one Select call, returning the
// nodes that remain eligible. A filter should return nodes it received
// rather than copies, so their weighted state is preserved.
type NodeFilter func(context.Context, []*Node) []*Node
