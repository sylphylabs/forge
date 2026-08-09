# Client Retry

Status: implemented

Last reviewed: August 9, 2026

## Purpose

This document defines the client retry middleware in `middleware/retry`: the
retryable-error judgment and its default set, the backoff scheme, the
per-call idempotency declaration, the per-operation policy table and its
dynamic governance, and the deliberate exclusions — stream retries and
node re-selection.

The governance rule table this builds on is defined in
[`dynamic-governance.md`](dynamic-governance.md). Diagnosis probes are
defined in [`diagnosis-probes.md`](diagnosis-probes.md).

## Decision

Retry is one client unary middleware with three injectable judgments and no
new mechanism:

```go
// middleware/retry
type Policy struct {
    Attempts    int           // max attempts including the first; 1 disables
    BaseBackoff time.Duration // first wait bound, doubled per attempt
    MaxBackoff  time.Duration // wait bound cap
}

func Client(opts ...Option) (middleware.UnaryMiddleware, error)

func WithPolicy(p Policy) Option
func WithRules(rules *governance.Rules[Policy]) Option
func WithRetryable(f func(ctx context.Context, err error) bool) Option

func Idempotent(ctx context.Context) context.Context
func IsIdempotent(ctx context.Context) bool
func DefaultRetryable(ctx context.Context, err error) bool

type Rule struct{ Attempts int; BaseBackoff, MaxBackoff string }
func ParseRule(v config.Value) (Policy, error)
```

The middleware re-invokes the wrapped handler while three gates all hold:
the policy in effect grants another attempt, the retryable predicate accepts
the error, and the call context can accommodate the next backoff. On give-up
the last attempt's error is returned unchanged — retry adds no error type of
its own, so callers match on the underlying failure exactly as they would
without the middleware.

`Client` returns an error, not just a middleware: a nil option, an invalid
static policy, an explicitly nil rule table or predicate are construction
bugs and are reported at the offending call, never repaired into defaults.

## The Default Retryable Set

Retrying is only safe when the failed attempt provably never executed on a
server. The default predicate admits exactly two error classes on that
basis, plus one more under an explicit caller declaration:

- **Connection establishment failures** — a `*net.OpError` with `Op ==
  "dial"` anywhere in the error chain. The connection never opened, so no
  request bytes left the client. This is the one class where non-execution
  is a physical fact rather than a protocol promise.
- **Service unavailable** — `errors.IsServiceUnavailable`, code 503, which
  is also what gRPC `Unavailable` maps to through the transport error
  conversion (`transport/http/status`). Both protocols document the
  condition as "the service is temporarily unable to process the request";
  Forge's own client emits it for node-selection failure
  (`NODE_NOT_FOUND`), and gRPC emits `Unavailable` for connection loss.
  The request was rejected, not run.
- **Timeouts** — `errors.IsGatewayTimeout`, code 504, which gRPC
  `DeadlineExceeded` maps to — **only when the call context carries the
  [Idempotent] declaration**. A timed-out request is ambiguous: the server
  may have executed it and the response was lost. Retrying it is duplicate
  execution unless the operation tolerates that, and only the caller knows.

Everything else is refused:

- 4xx client errors (400, 401, 403, 404, 409) are deterministic; the same
  request yields the same answer.
- 429 is the server shedding load; retrying amplifies the overload the
  server is trying to survive.
- 500 reports that the server ran the request and failed — execution
  happened, so the duplicate-execution ambiguity is resolved against
  retrying.
- Local `context.Canceled` and `context.DeadlineExceeded` mean the caller
  is gone or out of time; no declaration overrides them.

The predicate is injectable via `WithRetryable` for callers whose transport
or error taxonomy differs; `IsIdempotent` is exported so a custom predicate
can honor the same declaration.

## The Idempotency Declaration

`Idempotent(ctx)` marks the calls made with a context as safe to execute
more than once. The choice of a context marker over the alternatives is
deliberate and is the shape a future idempotency middleware must build on:

- **Per call site, not per middleware chain.** Idempotency is a property of
  an operation's semantics, not of a client. One client instance serves both
  idempotent reads and non-idempotent writes; a chain-level flag would force
  either two clients or a false declaration. The context travels with the
  call, so the declaration lives exactly where the operation is known —
  generated call wrappers, or a thin application facade over the client.
- **A boolean assertion, not a key.** The declaration says "repeat execution
  is safe", nothing more. It deliberately does not carry an idempotency key:
  key generation, propagation as metadata, and server-side deduplication are
  a separate middleware's whole job. That middleware can define
  `WithKey(ctx, key)` alongside this package's `Idempotent(ctx)` and treat a
  keyed call as implicitly declared; the two compose because both are
  context-scoped. What this package fixes for it is only the reading side:
  "declared idempotent" must be checkable via `IsIdempotent(ctx)` by any
  middleware in the chain.
- **Not transport metadata.** A header or gRPC metadata entry would leak the
  declaration onto the wire, where it means nothing to the server today and
  would harden into accidental protocol. The context value is process-local;
  a future middleware that wants to propagate intent to the server does so
  explicitly, as its own wire contract.

## Backoff

Waits follow exponential growth with full jitter: the wait after failed
attempt *n* is drawn uniformly from `[0, min(BaseBackoff·2ⁿ⁻¹, MaxBackoff))`.
Full jitter is the standard choice because it decorrelates the retry storms
of many clients that failed at the same moment; a deterministic or
half-jittered schedule re-synchronizes them at every step. The draw uses
`math/rand/v2` — no seeding, no locking, no dependency.

Both axes are bounded by construction: `Attempts` caps the count and
`MaxBackoff` caps each wait, and `Client`/`ParseRule` reject a policy with
retries enabled but no positive base or a cap below the base.

The context is consulted twice per retry: a done context stops immediately,
and a context whose remaining deadline cannot fit the drawn wait makes the
middleware give up without sleeping — sleeping into a deadline would burn
the caller's remaining budget to deliver a guaranteed failure. Note that the
transport clients' own timeout (`WithTimeout` on either client) spans all
attempts collectively, since the middleware runs inside it; a per-attempt
budget, if wanted, is the timeout middleware composed inside retry.

## Request Replay

Each attempt re-enters everything composed inside the retry middleware, and
what "resend the request" means differs by transport:

- **gRPC**: the middleware sits inside the interceptor; each attempt calls
  the invoker again, which re-marshals the request message. Values are
  plain; nothing needs rewinding.
- **HTTP**: the client encodes the body once and the first attempt consumes
  the reader. Before each retry the middleware restores the body from
  `Request.GetBody`, which `http.NewRequest` provides for the buffered
  readers the Forge client uses. A body without `GetBody` — a caller-built
  streaming request — cannot be replayed, so the retry is abandoned and the
  last error returned rather than resending a truncated request.

The HTTP hook is asserted structurally (`interface{ Request()
*http.Request }`), so the package depends on no concrete transport and any
transport exposing the capability participates.

Composition order matters and is documented on `Client`: retry goes
outermost among resilience middleware, so a circuit breaker composed inside
it observes every attempt — which is the per-attempt accounting a breaker
needs, and the pairing that gives retries a budget: once the breaker opens,
its rejections are 503s of the breaker's own, and the loop stops consuming
attempts against a dead endpoint.

## Governed Policies

`WithRules(*governance.Rules[Policy])` follows the pattern established by
the rate-limit and timeout retrofits: the policy in effect resolves per
call from the transport operation, `governance.Watch` plus `ParseRule` feed
the table from a config section, and an invalid snapshot — negative
attempts, zero or negative backoff, a cap below the base — is refused
wholesale while the previous policies keep serving.

```yaml
governance:
  retry:
    "*":
      attempts: 2
    /helloworld.Greeter/SayHello:
      attempts: 4
      base_backoff: 50ms
      max_backoff: 2s
```

A config `Rule` names only what it tightens; unset fields keep the default
policy's values (3 attempts, 100ms base, 1s cap). `attempts: 1` is the
supported way to disable retries for an operation at runtime.

`Policy` is a plain comparable struct, so the snapshot reported by
`governance.Probe(rules, nil)` serializes as-is; registering it under
`governance/retry` needs no describe function and no support from this
package.

The policy resolves once per call, at entry. Re-resolving between attempts
would let an update change a call's budget mid-flight, making "how many
attempts did this call get" depend on update timing rather than on any one
configuration document.

## Streams Are Not Retried

The middleware is unary-only, deliberately, mirroring the reasoning that
kept the circuit breaker unary-only. A stream is stateful in both
directions: by the time an error surfaces, messages have been sent and
received, application state has advanced on both ends, and "retry" would
mean re-establishing the stream and replaying every sent message — which
requires buffering an unbounded prefix and guessing whether the server
processed the messages it received before failing. No conservative default
exists; gRPC's own transparent retry handles only the narrow case where no
message reached the wire, and that case belongs to the transport, not to a
framework middleware. Callers who need resumable streams need
application-level sequencing (offsets, acks), which no generic middleware
can supply.

## Node Re-selection Is Not In This Middleware

Retrying on a different node is the natural wish for dial failures, and the
selector machinery already delivers most of it without retry's involvement:

- the gRPC client delegates balancing to the resolver/balancer pair, which
  tracks connectivity per subchannel and steers new attempts away from dead
  backends on its own;
- the HTTP client runs `selector.Select` inside `Client.do` — inside the
  retry loop — so each attempt re-selects, and the `DoneInfo` fed back per
  attempt lets weighted selectors (p2c, ewma) demote the failing node
  between attempts.

What retry does not do is force the next attempt onto a *different* node
(an "exclude previously tried" filter). That requires retry to thread
attempt history into the selector's filter chain, coupling the middleware
to selector internals for a benefit the feedback loop already approximates.
If the need materializes, the clean seam is a `selector.NodeFilter` that
reads attempt history from the call context — an addition to the selector
packages that retry could populate without new coupling. Deferred until a
concrete case demands it.

## Observability

Each retry emits one structured `warn` log through `log.WarnContext` —
operation, failed attempt number, chosen backoff, and the triggering error —
so a call that succeeded only on attempt three is visible without tracing.
The context-aware form lets `log.ContextWithAttrs` metadata (request IDs)
flow into the record.

A dedicated retry-statistics probe was considered and not built: the
governance probe already answers "what policy is in effect", the log
answers "what retried and why", and a counter probe would need cross-call
mutable state in the middleware — the first such state in any Forge
middleware — to report numbers that metrics middleware composed outside
retry can already derive per attempt. If T8.3-style diagnosis later grows a
generic counter facility, retry can adopt it; it should not grow one
privately.

## Alternatives Rejected

- **Retrying timeouts by default.** The most common retry misconfiguration
  in the wild: a timed-out non-idempotent request that executed
  server-side becomes a duplicate write. Excluded from the default set;
  admitted only under the explicit per-call declaration.
- **Retry budgets (client-wide retry-rate caps).** A budget needs shared
  mutable state across calls and another tunable; the same protection —
  stop hammering a failing dependency — is the circuit breaker's job, and
  the two compose today (breaker inside retry). Revisit only with evidence
  that composition is insufficient.
- **Per-attempt timeouts inside retry.** Duplicates the timeout middleware.
  Compose `timeout` inside `retry` instead; each attempt then gets its own
  deadline while the caller's context bounds the whole sequence.
- **A `Retryable()` marker on `errors.Error`.** Would put transport policy
  into the error model and let servers dictate client retry behavior
  implicitly. The judgment stays client-side, injectable, and
  context-aware.
- **Idempotency as a call option or client option.** A gRPC `CallOption` or
  HTTP `CallOption` reaches only one transport's call surface and cannot be
  read by middleware; a client-wide option declares too much. The context
  marker is transport-neutral, per-call, and readable by any middleware.
- **Hedged requests.** Sending a second attempt before the first fails
  trades load for latency and requires idempotency unconditionally plus
  cancellation plumbing. Out of scope for a conservative default; nothing
  in the policy shape precludes a separate hedging middleware later.
