package ratelimit

import (
	"context"

	"github.com/sylphylabs/forge/errors"
	internalratelimit "github.com/sylphylabs/forge/internal/ratelimit"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/governance"
	"github.com/sylphylabs/forge/transport"
)

// ErrLimitExceed is returned when a request exceeds the configured rate limit.
var ErrLimitExceed = errors.MustDefine(errors.KindResourceExhausted, errors.Domain, "RATE_LIMIT_EXCEEDED").
	Msg("request rejected because the rate limit was exceeded")

// DoneFunc records request completion.
type DoneFunc = internalratelimit.DoneFunc

// DoneInfo contains request completion metadata.
type DoneInfo = internalratelimit.DoneInfo

// Limiter is a rate limiter.
type Limiter = internalratelimit.Limiter

// Option is ratelimit option.
type Option func(*options)

// WithLimiter set Limiter implementation,
// default is bbr limiter
func WithLimiter(limiter Limiter) Option {
	return func(o *options) {
		o.limiter = limiter
	}
}

// WithRules sets an operation-keyed limiter table, letting the limiter in
// effect vary per operation and change at runtime. Each request resolves its
// transport operation against the table; requests outside a transport
// context resolve the empty operation and so receive the table's fallback.
//
// Feed the table with [governance.Watch] and [ParseRule] to drive limits
// from configuration without a restart. A rule table takes precedence over
// [WithLimiter]; the static limiter still serves any lookup that yields a
// nil limiter.
func WithRules(rules *governance.Rules[Limiter]) Option {
	return func(o *options) {
		o.rules = rules
	}
}

type options struct {
	limiter Limiter
	rules   *governance.Rules[Limiter]
}

// limiterFor resolves the limiter that governs the call in flight.
func (o *options) limiterFor(ctx context.Context) Limiter {
	if o.rules == nil {
		return o.limiter
	}
	var operation string
	if info, ok := transport.FromServerContext(ctx); ok {
		operation = info.Operation()
	}
	if l := o.rules.For(operation); l != nil {
		return l
	}
	return o.limiter
}

// Server ratelimiter middleware
func Server(opts ...Option) middleware.UnaryMiddleware {
	options := newOptions(opts...)
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			done, e := options.limiterFor(ctx).Allow()
			if e != nil {
				// rejected
				return nil, ErrLimitExceed
			}
			// allowed; done must fire exactly once on every exit, including a
			// panic, or the limiter's in-flight count never drains.
			defer func() { done(DoneInfo{Err: err}) }()
			return handler(ctx, req)
		}
	}
}

// newDefaultLimiter returns the limiter used when none is configured.
func newDefaultLimiter() Limiter {
	return internalratelimit.NewLimiter()
}
