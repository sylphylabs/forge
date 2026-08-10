// Package retry provides client middleware that re-invokes a failed unary
// call, with an injectable backoff curve — exponential full jitter by
// default — and a per-operation policy that can be governed at runtime.
//
// The default posture is conservative: a failed attempt is retried only when
// the transport proves it never reached a server, or when the caller declares
// with [Idempotent] that reaching one twice is harmless. An error that offers
// neither is left alone, because most failures cannot rule out that a server
// already executed the request. See [DefaultRetryable] for the exact rule.
package retry

import (
	"context"
	"fmt"
	"math/rand/v2"
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
//
// Attempts always governs. BaseBackoff and MaxBackoff parameterize the
// default [ExponentialJitter] curve; a [Backoff] injected with [WithBackoff]
// replaces the curve outright and those two fields no longer shape any wait.
type Policy struct {
	// Attempts is the maximum number of attempts for one call, including
	// the first. It must be at least 1; 1 disables retries.
	Attempts int
	// BaseBackoff bounds the wait before the first retry. Each subsequent
	// wait doubles the bound, and every wait is drawn uniformly from
	// [0, bound) — full jitter. Must be positive when Attempts > 1.
	// Ignored when a [Backoff] is injected.
	BaseBackoff time.Duration
	// MaxBackoff caps the wait bound. Must be at least BaseBackoff when
	// Attempts > 1. Ignored when a [Backoff] is injected.
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

// Backoff computes how long to wait before the next attempt. There is no one
// best curve — exponential full jitter, decorrelated jitter, linear growth
// and a constant interval each suit a different dependency — so the curve is
// replaceable via [WithBackoff] rather than fixed by this package.
//
// Next receives the 1-based number of the attempt that just failed: 1 for the
// wait before the first retry. Implementations must be safe for concurrent
// use, since one middleware instance serves every call on a client. A
// non-positive result waits not at all.
//
// Next deliberately takes neither a context nor the failing error. A curve is
// a function of attempt number alone; a decision that needs the error or the
// call is the retryable predicate's ([WithRetryable]), and mixing the two
// would give two places to express "do not retry this".
type Backoff interface {
	Next(attempt int) time.Duration
}

// ExponentialJitter is the default [Backoff]: the wait after failed attempt
// n is drawn uniformly from [0, min(Base·2ⁿ⁻¹, Max)) — exponential growth
// with full jitter. Full jitter decorrelates the retry storms of many clients
// that failed at the same moment, which a deterministic or half-jittered
// schedule re-synchronizes at every step.
//
// The zero value waits not at all. [Client] builds one from the [Policy] in
// effect, so most callers never construct this directly; construct it to pin
// a curve that governance cannot move.
type ExponentialJitter struct {
	// Base bounds the wait before the first retry.
	Base time.Duration
	// Max caps the wait bound.
	Max time.Duration

	// randN draws from [0, n). Nil means math/rand/v2, which needs no
	// seeding and no locking; tests inject a deterministic draw.
	randN func(n int64) int64
}

// Next implements [Backoff].
func (e ExponentialJitter) Next(attempt int) time.Duration {
	bound := e.bound(attempt)
	if bound <= 0 {
		return 0
	}
	draw := e.randN
	if draw == nil {
		draw = rand.Int64N
	}
	return time.Duration(draw(int64(bound)))
}

// bound returns the full-jitter bound for the wait after the given 1-based
// failed attempt: Base doubled per prior attempt, capped at Max.
func (e ExponentialJitter) bound(attempt int) time.Duration {
	d := e.Base
	for i := 1; i < attempt && d < e.Max; i++ {
		d <<= 1
		if d <= 0 { // overflow
			return e.Max
		}
	}
	return min(d, e.Max)
}

// idempotentKey marks a context whose call may safely execute more than once.
type idempotentKey struct{}

// Idempotent returns a context that declares the calls made with it safe to
// execute more than once. [DefaultRetryable] then also retries the failures
// that cannot prove delivery either way — unavailability and timeouts — which
// are otherwise excluded because the request may have reached a server and
// executed. The declaration is per call site and per caller: it
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
// not set. A retry re-executes work, so it demands one of two justifications:
// evidence that the attempt never reached a server, or the caller's
// declaration that reaching one twice is harmless. An error carrying neither
// is not retried.
//
// Evidence comes from the transport. A transport that can prove a request
// never left this process marks the error with [transport.MarkNotSent], and
// such an error is retried whatever its Kind and whatever the caller declared:
// work that never started cannot be duplicated by starting it.
//
// Declaration comes from the call site. When ctx carries [Idempotent], the
// caller asserts the operation tolerates repeat execution, and these Kinds are
// then retried:
//
//   - [errors.KindUnavailable]: the transport or server reports itself unable
//     to serve, a condition both protocols document as transient;
//   - [errors.KindDeadlineExceeded]: the attempt ran out of time, which says
//     nothing about whether a server executed it.
//
// Both Kinds are ambiguous about delivery on their own — a service can go
// unavailable after executing a request and before its reply arrives — which
// is why neither is retried without the declaration.
//
// Everything else is not retried. Caller-side failures are deterministic;
// retrying them cannot succeed. [errors.KindInternal] reports that the server
// ran the request and failed. [errors.KindResourceExhausted] and
// [errors.KindConflict] stay out even under the declaration: retrying a
// rate-limited call without backoff makes overload worse, and retrying a
// conflict needs the caller to re-read state first. Local context
// cancellation and deadline expiry are never retryable, since no declaration
// makes a caller's own abandonment worth repeating.
func DefaultRetryable(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if transport.WasNotSent(err) {
		return true
	}
	if !IsIdempotent(ctx) {
		return false
	}
	switch errors.KindOf(err) {
	case errors.KindUnavailable, errors.KindDeadlineExceeded:
		return true
	default:
		return false
	}
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

// WithBackoff replaces the default [ExponentialJitter] curve with a custom
// one, for callers whose dependency wants decorrelated jitter, linear growth,
// a constant interval, or a schedule read from elsewhere.
//
// An injected curve is fixed for the life of the middleware and applies to
// every operation. It takes over the wait entirely, jitter included: the
// middleware sleeps exactly what [Backoff.Next] returns. Consequently the
// BaseBackoff and MaxBackoff of the [Policy] in effect stop shaping waits —
// including the per-operation policies a rule table serves, which continue to
// govern Attempts and nothing else. A curve that should follow governance
// must read the governed values itself; this package does not thread a
// per-call policy into Next, because a per-call curve and a governed curve
// are two mechanisms for one knob.
//
// [Client] rejects a nil Backoff.
func WithBackoff(b Backoff) Option {
	return func(o *options) {
		o.backoff = b
		o.backoffSet = true
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
	backoff      Backoff
	backoffSet   bool

	// test seams; production values are set by Client.
	sleep func(ctx context.Context, d time.Duration) error
	randN func(n int64) int64
}

// backoffFor returns the curve for a call served under policy p: the injected
// one when set, otherwise the default curve parameterized by that policy — so
// a governed BaseBackoff/MaxBackoff reaches the default curve, and only the
// default curve.
func (o *options) backoffFor(p Policy) Backoff {
	if o.backoff != nil {
		return o.backoff
	}
	return ExponentialJitter{Base: p.BaseBackoff, Max: p.MaxBackoff, randN: o.randN}
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
// option, an invalid static policy, an explicitly nil rule table, retryable
// predicate, or backoff.
func Client(opts ...Option) (middleware.UnaryMiddleware, error) {
	o := &options{
		policy:    defaultPolicy,
		retryable: DefaultRetryable,
		sleep:     sleepContext,
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
	if o.backoffSet && o.backoff == nil {
		return nil, fmt.Errorf("retry: WithBackoff requires a non-nil backoff")
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			p := o.policyFor(ctx)
			backoff := o.backoffFor(p)
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
				wait := max(backoff.Next(attempt), 0)
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
