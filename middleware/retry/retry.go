// Package retry provides client middleware that re-invokes a failed unary
// call, with exponential full-jitter backoff and a per-operation policy that
// can be governed at runtime.
//
// The default posture is conservative: only errors that prove the request
// never executed on a server — connection establishment failures and
// service-unavailable rejections — are retried. Timeout-class errors are
// ambiguous (the request may have executed) and are retried only for calls
// the caller has declared idempotent with [Idempotent]. See
// [DefaultRetryable] for the exact set and its basis.
package retry

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"net/http"
	"time"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/log"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/governance"
	"github.com/sylphylabs/forge/transport"
)

// Policy is the retry policy for one operation: how many attempts a call may
// use and how the wait between them grows.
type Policy struct {
	// Attempts is the maximum number of attempts for one call, including
	// the first. It must be at least 1; 1 disables retries.
	Attempts int
	// BaseBackoff bounds the wait before the first retry. Each subsequent
	// wait doubles the bound, and every wait is drawn uniformly from
	// [0, bound) — full jitter. Must be positive when Attempts > 1.
	BaseBackoff time.Duration
	// MaxBackoff caps the wait bound. Must be at least BaseBackoff when
	// Attempts > 1.
	MaxBackoff time.Duration
}

// defaultPolicy backs [Client] when no policy is set and fills unset fields
// of a config [Rule].
var defaultPolicy = Policy{
	Attempts:    3,
	BaseBackoff: 100 * time.Millisecond,
	MaxBackoff:  time.Second,
}

// Validate reports whether the policy is safe to serve. It rejects a policy
// that could not have been produced by [ParseRule]: fewer than one attempt,
// or — when retries are enabled — a non-positive base backoff or a cap below
// the base. Call it before installing policies with [governance.Rules.Replace],
// which by design validates nothing.
func (p Policy) Validate() error {
	if p.Attempts < 1 {
		return fmt.Errorf("retry: attempts must be at least 1, got %d", p.Attempts)
	}
	if p.Attempts == 1 {
		return nil
	}
	if p.BaseBackoff <= 0 {
		return fmt.Errorf("retry: base backoff must be positive, got %s", p.BaseBackoff)
	}
	if p.MaxBackoff < p.BaseBackoff {
		return fmt.Errorf("retry: max backoff %s must not be below base backoff %s", p.MaxBackoff, p.BaseBackoff)
	}
	return nil
}

// backoffBound returns the full-jitter bound for the wait after the given
// 1-based failed attempt: BaseBackoff doubled per prior attempt, capped at
// MaxBackoff.
func (p Policy) backoffBound(attempt int) time.Duration {
	d := p.BaseBackoff
	for i := 1; i < attempt && d < p.MaxBackoff; i++ {
		d <<= 1
		if d <= 0 { // overflow
			return p.MaxBackoff
		}
	}
	return min(d, p.MaxBackoff)
}

// idempotentKey marks a context whose call may safely execute more than once.
type idempotentKey struct{}

// Idempotent returns a context that declares the calls made with it safe to
// execute more than once. [DefaultRetryable] then also retries timeout-class
// errors, which are otherwise excluded because the request may have reached
// a server and executed. The declaration is per call site and per caller: it
// asserts a property of the operation being invoked, so apply it where the
// operation is known — typically generated call wrappers or a thin client
// facade.
func Idempotent(ctx context.Context) context.Context {
	return context.WithValue(ctx, idempotentKey{}, true)
}

// IsIdempotent reports whether ctx carries the [Idempotent] declaration.
// Custom retryable predicates can use it to widen their own sets the same
// way [DefaultRetryable] does.
func IsIdempotent(ctx context.Context) bool {
	b, _ := ctx.Value(idempotentKey{}).(bool)
	return b
}

// DefaultRetryable is the retryable predicate used when [WithRetryable] is
// not set. It is deliberately conservative: an error is retryable only when
// the failed attempt provably never executed on a server, or when the caller
// has declared repeat execution safe.
//
// The default set is:
//
//   - connection establishment failures — a *net.OpError with Op "dial"
//     anywhere in the chain: the connection never opened, so no request was
//     sent;
//   - service-unavailable errors — [errors.IsServiceUnavailable], HTTP 503
//     and gRPC Unavailable: the transport or server rejected the request
//     before executing it, and both protocols document the condition as
//     transient;
//   - timeout errors — [errors.IsGatewayTimeout], HTTP 504 and gRPC
//     DeadlineExceeded — only when ctx carries the [Idempotent] declaration:
//     a timed-out request may have executed, so retrying it is duplicate
//     execution unless the operation tolerates that.
//
// Everything else is not retried. Client errors (400, 401, 403, 404, 409,
// 429) are deterministic or load-induced; retrying them cannot succeed or
// makes overload worse. Internal errors (500) report that the server ran the
// request and failed. Local context cancellation and deadline expiry are
// never retryable regardless of any declaration.
func DefaultRetryable(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var oe *net.OpError
	if errors.As(err, &oe) && oe.Op == "dial" {
		return true
	}
	if errors.IsServiceUnavailable(err) {
		return true
	}
	return IsIdempotent(ctx) && errors.IsGatewayTimeout(err)
}

// Option is retry option.
type Option func(*options)

// WithPolicy sets the static policy applied to every call. It is the
// fallback when no rule table is set or a lookup yields no usable policy.
// The default is 3 attempts with backoff drawn from 100ms doubling to a 1s
// cap. [Client] rejects an invalid policy.
func WithPolicy(p Policy) Option {
	return func(o *options) {
		o.policy = p
	}
}

// WithRules sets an operation-keyed policy table, letting the policy in
// effect vary per operation and change at runtime. Each call resolves its
// transport operation against the table; calls outside a transport context
// resolve the empty operation and so receive the table's fallback.
//
// Feed the table with [governance.Watch] and [ParseRule] to drive retry
// policies from configuration without a restart. A rule table takes
// precedence over [WithPolicy]; the static policy still serves any lookup
// that yields a policy with fewer than one attempt. A call resolves its
// policy once when it starts; an update applies from the next call.
func WithRules(rules *governance.Rules[Policy]) Option {
	return func(o *options) {
		o.rules = rules
		o.rulesSet = true
	}
}

// WithRetryable replaces [DefaultRetryable] as the judgment of which errors
// are worth another attempt. The predicate receives the call context, so it
// can honor the [Idempotent] declaration via [IsIdempotent] or consult its
// own markers. Regardless of the predicate, no retry happens once the call
// context is canceled or its deadline cannot accommodate the next backoff.
// [Client] rejects a nil predicate.
func WithRetryable(f func(ctx context.Context, err error) bool) Option {
	return func(o *options) {
		o.retryable = f
		o.retryableSet = true
	}
}

type options struct {
	policy       Policy
	rules        *governance.Rules[Policy]
	rulesSet     bool
	retryable    func(context.Context, error) bool
	retryableSet bool

	// test seams; production values are set by Client.
	sleep func(ctx context.Context, d time.Duration) error
	randN func(n int64) int64
}

// policyFor resolves the policy for the call in flight.
func (o *options) policyFor(ctx context.Context) Policy {
	if o.rules == nil {
		return o.policy
	}
	var operation string
	if tr, ok := transport.FromClientContext(ctx); ok {
		operation = tr.Operation()
	}
	if p := o.rules.For(operation); p.Attempts >= 1 {
		return p
	}
	return o.policy
}

// Client returns middleware that re-invokes the call while the policy in
// effect grants attempts, the error is retryable, and the context allows
// waiting out the next backoff. On give-up, the last attempt's error is
// returned unchanged.
//
// The middleware never retries past the call context: it gives up as soon as
// the context is done or its remaining deadline cannot accommodate the next
// backoff. Note that a transport-level client timeout spans all attempts of
// a call collectively, so it bounds the whole retry sequence, not each
// attempt.
//
// Every attempt re-enters everything composed inside this middleware, so
// compose retry outermost among resilience middleware: a circuit breaker
// inside retry observes each attempt, which is the accounting it needs.
//
// Client returns an error for a configuration that cannot serve: a nil
// option, an invalid static policy, an explicitly nil rule table or
// retryable predicate.
func Client(opts ...Option) (middleware.UnaryMiddleware, error) {
	o := &options{
		policy:    defaultPolicy,
		retryable: DefaultRetryable,
		sleep:     sleepContext,
		randN:     rand.Int64N,
	}
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("retry: nil option at index %d", i)
		}
		opt(o)
	}
	if err := o.policy.Validate(); err != nil {
		return nil, err
	}
	if o.rulesSet && o.rules == nil {
		return nil, fmt.Errorf("retry: WithRules requires a non-nil rule table")
	}
	if o.retryableSet && o.retryable == nil {
		return nil, fmt.Errorf("retry: WithRetryable requires a non-nil predicate")
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			p := o.policyFor(ctx)
			var lastErr error
			for attempt := 1; ; attempt++ {
				reply, err := handler(ctx, req)
				if err == nil {
					return reply, nil
				}
				lastErr = err
				if attempt >= p.Attempts || ctx.Err() != nil || !o.retryable(ctx, err) {
					return nil, lastErr
				}
				var wait time.Duration
				if bound := p.backoffBound(attempt); bound > 0 {
					wait = time.Duration(o.randN(int64(bound)))
				}
				if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < wait {
					return nil, lastErr
				}
				if !rewindRequest(ctx) {
					return nil, lastErr
				}
				logRetry(ctx, attempt, wait, err)
				if o.sleep(ctx, wait) != nil {
					return nil, lastErr
				}
			}
		}
	}, nil
}

// logRetry reports one retry decision: the attempt that failed, the wait
// about to be taken, and the error that triggered the retry.
func logRetry(ctx context.Context, attempt int, wait time.Duration, err error) {
	var operation string
	if tr, ok := transport.FromClientContext(ctx); ok {
		operation = tr.Operation()
	}
	log.WarnContext(ctx, "retry: retrying call",
		"operation", operation,
		"failed_attempt", attempt,
		"backoff", wait,
		"error", err,
	)
}

// sleepContext waits d or until ctx is done, whichever comes first.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// httpRequester is the capability an HTTP client transport exposes for
// reaching the request in flight. It is asserted structurally so this
// package does not depend on a concrete transport.
type httpRequester interface {
	Request() *http.Request
}

// rewindRequest makes the attempt's request re-sendable, reporting false
// when it cannot. A gRPC attempt re-marshals its request message, so there
// is nothing to rewind. An HTTP attempt consumed the request body, which
// must be restored from Request.GetBody; a body without GetBody cannot be
// replayed, so the retry is abandoned rather than sent truncated.
func rewindRequest(ctx context.Context) bool {
	tr, ok := transport.FromClientContext(ctx)
	if !ok {
		return true
	}
	hr, ok := tr.(httpRequester)
	if !ok {
		return true
	}
	r := hr.Request()
	if r == nil || r.Body == nil {
		return true
	}
	if r.GetBody == nil {
		return false
	}
	body, err := r.GetBody()
	if err != nil {
		return false
	}
	r.Body = body
	return true
}
