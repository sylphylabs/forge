// Package ratelimit provides server middleware that sheds load when the
// service is over capacity, failing rejected requests fast with
// [ErrLimitExceed] (KindResourceExhausted) instead of queueing them into
// timeouts.
//
// [Server] uses an adaptive BBR limiter by default — admission is driven by
// measured CPU, throughput, and latency rather than a fixed rate. Supply a
// different [Limiter] with [WithLimiter], or an operation-keyed rule table
// with [WithRules] to vary and re-tune limiters at runtime from config (see
// docs/design/dynamic-governance.md). [ServerStream] charges one unit per
// stream lifecycle; [PerMessageServerStream] charges per received message —
// pick by which cost the limiter is protecting.
package ratelimit
