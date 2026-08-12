// Package selector picks one node from a discovered set for each request:
// load balancing on the client side of Forge's HTTP and gRPC transports.
//
// A [Selector] filters candidate [Node]s through [NodeFilter]s, asks a
// [Balancer] to pick one, and returns a callback that feeds the outcome —
// latency, error — back to the balancer's weighting. A [Builder] constructs
// selectors; transports accept one through their WithSelector client option.
// [Composite] assembles a selector from any filter set and balancer, and the
// subpackages supply the balancing policies: wrr (weighted round robin, the
// client default), p2c (power of two choices), and random. Version-based
// node filtering lives in the filter subpackage, node weighting in node/.
//
// [ErrNoAvailable] is returned when filtering leaves no candidate to pick
// from. It classifies as KindUnavailable, so callers match it like any other
// Forge error.
package selector
