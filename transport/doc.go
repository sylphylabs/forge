// Package transport defines the contract between the application lifecycle
// and the servers it runs, and the call-scoped view middleware gets of the
// transport underneath it.
//
// [Server] is all the lifecycle requires: Start and Stop. Everything else is
// an optional capability discovered by type assertion — [Endpointer] for the
// address to publish to a registry, [Healthzer] for readiness,
// [GracefulStopper] for draining, [ReplyHeaderer] for a mutable reply
// header. A server that does not implement one makes no claim, and consumers
// must not require more than they need.
//
// [Transporter] is what middleware sees: the transport [Kind], the opaque
// Operation string, the endpoint, and the request header. It travels in the
// context — [FromServerContext] on the serving side, [FromClientContext] on
// the calling side. Kind is an open type: transports outside this module
// declare their own.
//
// [MarkNotSent] and [WasNotSent] carry delivery evidence: a transport marks
// an error only when it can prove the request never left the process, which
// is what the retry middleware needs to retry safely without an idempotence
// declaration (see docs/design/retry.md).
//
// The concrete transports live in the subpackages http, grpc, and message.
package transport
