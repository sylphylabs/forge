// Package metadata provides middleware that moves application metadata
// (package github.com/sylphylabs/forge/metadata) between the context and the
// transport headers, so request-scoped values propagate across process
// boundaries without transport-specific code.
//
// [Server] reads propagated headers off the incoming request into the server
// metadata context; [Client] writes constants and propagated values onto the
// outgoing request; [ServerStream] is the stream-side equivalent. Which keys
// propagate is decided by prefix — "x-md-" by default, configurable with
// [WithPropagatedPrefix] — and [WithConstants] attaches fixed pairs to every
// outgoing call.
//
// Values that cannot travel as header values (non-ASCII, control bytes) are
// percent-escaped on the wire and decoded transparently on receipt.
package metadata
