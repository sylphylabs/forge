// Package circuitbreaker provides client middleware that stops calling an
// operation whose recent attempts have been failing, so a struggling
// dependency gets headroom to recover instead of more load.
//
// [Client] maintains one breaker per operation, created lazily; supply your
// own implementation with [WithBreakerFactory]. A rejected request fails
// fast with [ErrNotAllowed], which classifies as KindUnavailable.
//
// Attach it with the transport's WithClientMiddleware option; see
// docs/agent/middleware.md.
package circuitbreaker
