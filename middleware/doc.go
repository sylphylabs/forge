// Package middleware defines the two middleware contracts every Forge
// transport composes: unary and stream.
//
// [UnaryMiddleware] wraps a [UnaryHandler], one request producing one reply.
// [StreamMiddleware] wraps a [StreamHandler], one complete stream lifecycle —
// not one message; per-message behaviour comes from decorating the
// [ServerStream] the handler receives. There is no single combined
// middleware type.
//
// Middleware attaches in three places, all at construction time: server-wide
// through each transport server's WithMiddleware option, per-service and
// per-method through the plan types protoc-gen-go-middleware generates, and
// client-side through each client's WithClientMiddleware option. [ChainUnary]
// and [ChainStream] compose without validation; [ComposeUnary] and
// [ComposeStream] validate and are what generated wrappers call.
//
// The middleware implementations Forge ships live in the subpackages —
// recovery, logging, validate, ratelimit, timeout, metadata, retry,
// circuitbreaker, selector — and tracing in the separate contrib/otel module.
// See docs/agent/middleware.md for the usage contract and
// docs/design/generated-middleware.md for the rationale.
package middleware
