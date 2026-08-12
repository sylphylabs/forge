// Package logging provides middleware that writes one structured record per
// request: operation, transport kind, request summary, latency, and — on
// failure — the error's kind, reason, domain, and trace ID.
//
// [Server] and [Client] cover unary calls on their respective sides;
// [ServerStream] covers one record per stream lifecycle. All take a
// *slog.Logger and fall back to the process default when given nil. A
// request type that implements [Redacter] controls its own logged
// representation, which is how sensitive fields stay out of log output.
//
// Failures are logged by kind and reason rather than transport status codes,
// and the record includes the full local error with its cause chain — the
// detail that deliberately never crosses the wire (see docs/agent/errors.md).
package logging
