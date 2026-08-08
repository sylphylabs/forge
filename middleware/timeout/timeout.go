// Package timeout provides server middleware that bounds handler execution
// time, with a per-operation deadline that can be governed at runtime.
package timeout

import (
	"context"
	"fmt"
	"time"

	"github.com/sylphylabs/forge/config"
	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/governance"
	"github.com/sylphylabs/forge/transport"
)

// ErrTimeout is returned when the handler misses the configured deadline.
var ErrTimeout = errors.GatewayTimeout("TIMEOUT", "request timed out")

// Option is timeout option.
type Option func(*options)

// WithTimeout sets the static deadline applied to every request. It is the
// fallback when no rule table is set or a lookup yields zero. The default is
// one second; a negative value is treated as zero, which disables the
// deadline.
func WithTimeout(d time.Duration) Option {
	return func(o *options) {
		o.timeout = d
	}
}

// WithRules sets an operation-keyed deadline table, letting the deadline in
// effect vary per operation and change at runtime. Each request resolves its
// transport operation against the table; requests outside a transport
// context resolve the empty operation and so receive the table's fallback.
//
// Feed the table with [governance.Watch] and [ParseRule] to drive deadlines
// from configuration without a restart. A rule table takes precedence over
// [WithTimeout]; the static deadline still serves any lookup that yields
// zero.
func WithRules(rules *governance.Rules[time.Duration]) Option {
	return func(o *options) {
		o.rules = rules
	}
}

type options struct {
	timeout time.Duration
	rules   *governance.Rules[time.Duration]
}

// timeoutFor resolves the deadline for the call in flight.
func (o *options) timeoutFor(ctx context.Context) time.Duration {
	if o.rules == nil {
		return o.timeout
	}
	var operation string
	if info, ok := transport.FromServerContext(ctx); ok {
		operation = info.Operation()
	}
	if d := o.rules.For(operation); d > 0 {
		return d
	}
	return o.timeout
}

// Server returns middleware that cancels the request context once the
// deadline in effect elapses. A handler that honors context cancellation
// then returns [ErrTimeout] to the caller; context.DeadlineExceeded escaping
// the handler is mapped to the same error. A zero deadline leaves the
// context untouched.
//
// The middleware shortens the deadline, never extends it: an earlier
// deadline already on the context wins, and its error is not remapped.
func Server(opts ...Option) middleware.UnaryMiddleware {
	options := &options{timeout: time.Second}
	for _, o := range opts {
		o(options)
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			d := options.timeoutFor(ctx)
			if d <= 0 {
				return handler(ctx, req)
			}
			tctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			reply, err := handler(tctx, req)
			if err != nil && errors.Is(err, context.DeadlineExceeded) &&
				tctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
				return nil, ErrTimeout
			}
			return reply, err
		}
	}
}

// ParseRule reads one config rule as a Go duration string, such as "300ms",
// for use as the parse function of [governance.Watch]. It rejects values
// that would be unsafe to serve — unparseable strings and non-positive
// durations — so that an invalid snapshot is refused as a whole and the
// previously installed deadlines keep governing traffic.
func ParseRule(v config.Value) (time.Duration, error) {
	s, err := v.String()
	if err != nil {
		return 0, fmt.Errorf("timeout rule: %w", err)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("timeout rule: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("timeout rule: must be positive, got %s", d)
	}
	return d, nil
}
