# Client Retry

Status: accepted

Last reviewed: August 10, 2026

## Purpose

This document defines the client retry middleware in `middleware/retry`: the
retry decision and the delivery evidence it rests on, the injectable backoff
curve, the per-call idempotency declaration, the per-operation policy table
and its dynamic governance, and the deliberate exclusions — stream retries
and node re-selection.

The governance rule table this builds on is defined in
[`dynamic-governance.md`](dynamic-governance.md). Diagnosis probes are
defined in [`diagnosis-probes.md`](diagnosis-probes.md).

## Decision

Retry is one client unary middleware with injectable judgments and no new
mechanism:

```go
// middleware/retry
type Policy struct {
    Attempts    int           // max attempts including the first; 1 disables
    BaseBackoff time.Duration // first wait bound of the default curve
    MaxBackoff  time.Duration // wait bound cap of the default curve
}

type Backoff interface {
    Next(attempt int) time.Duration
}

type ExponentialJitter struct{ Base, Max time.Duration }

func Client(opts ...Option) (middleware.UnaryMiddleware, error)

func WithPolicy(p Policy) Option
func WithRules(rules *governance.Rules[Policy]) Option
func WithRetryable(f func(ctx context.Context, err error) bool) Option
func WithBackoff(b Backoff) Option

func Idempotent(ctx context.Context) context.Context
func IsIdempotent(ctx context.Context) bool
func DefaultRetryable(ctx context.Context, err error) bool

type Rule struct{ Attempts int; BaseBackoff, MaxBackoff string }
func ParseRule(v config.Value) (Policy, error)
```

The middleware re-invokes the wrapped handler while three gates all hold:
the policy in effect grants another attempt, the retry decision accepts the
error in the context of this operation, and the call context can accommodate
the next backoff. On give-up
the last attempt's error is returned unchanged — retry adds no error type of
its own, so callers match on the underlying failure exactly as they would
without the middleware.

`Client` returns an error, not just a middleware: a nil option, an invalid
static policy, an explicitly nil rule table, predicate, or backoff are
construction bugs and are reported at the offending call, never repaired
into defaults.

## The Retry Decision

A retry re-executes work. Whether that is safe turns on three things: what
class of failure occurred, whether the request ever reached a server, and
whether the operation tolerates running twice. An `errors.Kind` answers only
the first. The second is known solely to the transport, and the third solely
to the call site, so the decision composes all three rather than deriving
from any one.

`DefaultRetryable` retries a failure that carries **delivery evidence** or an
**idempotence declaration**, and nothing else.

### Evidence: the transport proved nothing was sent

A transport that can prove a request never left this process marks its error:

```go
// transport package
func MarkNotSent(err error) error
func WasNotSent(err error) bool
```

A marked error is retried whatever its Kind and whatever the caller declared:
work that never started cannot be duplicated by starting it. This is the only
route by which a non-idempotent call earns an automatic retry.

Marking is an assertion of proof, so it is made conservatively. A transport
marks only what it can demonstrate, and the absence of a mark reads as "the
request may have executed" — the safe assumption. `WasNotSent` returning false
therefore means no transport made the claim, not that delivery occurred.

The HTTP client marks three failures. Node selection failing means no
connection was attempted. A dial failure — a `*net.OpError` with `Op == "dial"`
in the chain — means `net/http` had no connection to write to, since it writes
a request only after establishing one; an empty connection pool funnels into
the same dial. A failed WebSocket handshake means the stream never carried an
application message.

It deliberately leaves other failures unmarked. A request that was written and
then lost its connection surfaces as a bare `io.EOF` under `*url.Error`, which
at that layer is indistinguishable from a server that executed the request and
failed to reply. A response-side read error arrives with `Op == "read"`, by
which point delivery has certainly happened.

The gRPC client marks nothing. grpc-go reports a call that never found a
listener and a call whose connection died mid-flight as the same thing: a
`codes.Unavailable` status error with no `Unwrap` method, no typed cause and no
status detail, separated only by free text inside the message. That text is an
unstable internal string, and even reading it could not rule out the case that
matters — a request delivered and executed whose reply was lost. Marking on
that basis would assert proof this transport does not have, so gRPC calls rely
on the declaration instead.

### Declaration: the caller says repetition is harmless

When the context carries `Idempotent`, these Kinds are retried:

- `KindUnavailable` — the transport or server reports itself unable to serve,
  a condition both protocols document as transient.
- `KindDeadlineExceeded` — the attempt ran out of time, which says nothing
  about whether a server executed it.

Both are ambiguous about delivery on their own. A service can go unavailable
after executing a request and before its reply arrives, so neither is retried
without the declaration.

### Everything else

`DefaultRetryable` evaluates in this order:

1. A nil error is not retryable.
2. Local context cancellation and deadline expiry (`context.Canceled`,
   `context.DeadlineExceeded` anywhere in the chain) are refused
   unconditionally, before any other test. The caller's own budget is spent;
   another attempt cannot outrun it, and no declaration overrides this.
3. An error carrying delivery evidence is accepted.
4. Absent the `Idempotent` declaration, the error is refused.
5. Under the declaration, `KindUnavailable` and `KindDeadlineExceeded` are
   accepted.
6. Every other Kind is refused.

The refusals are refusals on the merits, not omissions:

- `KindInvalidArgument`, `KindFailedPrecondition`, `KindOutOfRange`,
  `KindUnauthenticated`, `KindPermissionDenied`, `KindNotFound`,
  `KindAlreadyExists`, `KindUnimplemented` — caller-side or deterministic.
  The same request produces the same failure; retrying only multiplies load.
- `KindInternal`, `KindDataLoss` — the server ran the request and something
  broke. Repetition re-runs a request that already had an effect, against a
  server in a state it did not expect.
- `KindCanceled` — the peer that wanted the answer is gone.
- `KindUnknown` — unclassified. An unclassified failure has not proven it was
  not executed, so the conservative default declines.
- `KindResourceExhausted` — a rate limit or quota. Retrying without server
  retry guidance makes overload worse, which is exactly the failure mode a
  blanket default must not create.
- `KindConflict` — optimistic concurrency lost. The caller has to re-read
  state before a second attempt means anything; replaying the same request
  will lose again.

The last two stay refused even under the declaration: idempotence makes
repetition safe for correctness, not free for the server.

The cost of this posture is that a transport unable to prove delivery gives
its non-idempotent calls no automatic retry at all. That is the intended
trade: safety is the default, and convenience takes one explicit declaration.
Callers whose protocol supplies proof the framework cannot see inject
`WithRetryable`, and `IsIdempotent` lets that predicate honor the same
declaration.

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

The curve is an interface, and the framework ships one implementation as the
default rather than as the only option:

```go
type Backoff interface {
    Next(attempt int) time.Duration
}
```

**Why injectable.** There is no single best retry curve. Exponential full
jitter is right for a dependency failing under load-driven contention;
decorrelated jitter converges faster on recovery; a constant interval is
right for a dependency that recovers on a fixed cycle, where exponential
growth just adds latency to a wait whose length was never the problem; linear
growth suits a queue draining at a known rate. Picking one and hard-coding it
would make the framework's guess unavoidable for every caller whose
dependency does not match it, when the seam that removes the guess is one
method wide.

**Why this shape.** `Next` takes the 1-based number of the attempt that just
failed and returns the wait, and takes nothing else:

- *No `context.Context`.* A curve is a pure function of attempt number.
  Passing the context would invite curves that read request state and make
  the wait depend on the call, which is unreproducible in the one place — a
  retry storm — where reasoning about waits matters. Context concerns are
  already handled by the middleware around the curve, which refuses to sleep
  past the deadline.
- *No error.* Whether a failure deserves another attempt is `WithRetryable`'s
  question, already answered before the curve is consulted. Letting `Next`
  see the error would create a second place to express "do not retry this" —
  by returning an absurd wait — with the two able to disagree.
- *Returns the final wait, not a bound to be jittered.* Jitter is part of a
  curve's identity, not a decoration applied to it. If the middleware drew a
  random value inside a returned bound, a constant-interval implementation
  could not express a constant interval. `ExponentialJitter` therefore owns
  its own draw, from `math/rand/v2` — no seeding, no locking, no dependency.

Implementations must be concurrency-safe: one middleware instance serves
every call on a client. A non-positive result means no wait.

**Interaction with `Policy` and governance.** `Policy.Attempts` always
governs — it is the middleware's own budget, checked before the curve is
consulted at all. `BaseBackoff` and `MaxBackoff` are parameters *of the
default curve*: when no curve is injected, the middleware builds an
`ExponentialJitter` from the policy resolved for that call, so a governed
rule that widens `base_backoff` reaches the default curve without ceremony.

Injecting a curve overrides those two fields, including the values a rule
table serves; such a table then governs `Attempts` and nothing else. This is
the intended and only sensible reading — an injected curve computes waits
itself, so there is nothing for a base and a cap to parameterize. It is
documented on `WithBackoff` because the silent alternative would be a
governance knob that appears to work and does not.

The rejected alternative was threading the resolved `Policy` into `Next` so a
custom curve could follow governance. It makes every implementation accept
parameters most of them ignore, and it creates two channels for one knob: an
operator editing `base_backoff` would change waits or not depending on
whether the injected curve chose to honor it. A curve that must be governed
reads the governed values itself, through the same config the rule table
uses, and owns that contract explicitly.

Both axes stay bounded by construction: `Attempts` caps the count, the
default curve's `Max` caps each wait, and `Client`/`ParseRule` reject a policy
with retries enabled but no positive base or a cap below the base. `Client`
rejects a nil `Backoff` at construction, so a misconfigured curve fails at the
call that built the middleware rather than as a nil dereference on the first
retry in production.

The context is consulted twice per retry: a done context stops immediately,
and a context whose remaining deadline cannot fit the drawn wait makes the
middleware give up without sleeping — sleeping into a deadline would burn
the caller's remaining budget to deliver a guaranteed failure. Note that the
transport clients' own timeout (`WithRequestTimeout` on either client) spans all
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
its rejections are `KindUnavailable` errors of the breaker's own, returned
without touching the endpoint, so the remaining attempts drain against a
local check instead of a dead dependency.

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
supported way to disable retries for an operation at runtime. The two backoff
fields reach the default curve only; see [Backoff](#backoff) for what a rule
governs once a curve is injected.

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
- **Deriving retryability from the error Kind alone.** A Kind classifies a
  failure in isolation: it sees neither the idempotence declaration nor the
  transport's delivery evidence. `KindUnavailable` in particular covers two
  opposite situations — a connection that never opened, and a server that
  executed the request before its reply was lost — so a predicate reading only
  the Kind would authorize the framework to repeat a non-idempotent call on
  the strength of a failure that proves nothing. The judgment stays
  client-side, injectable, and context-aware.
- **A per-error retry hint on the wire.** A server-set "retryable" flag on
  the error message would let servers dictate client retry behavior
  implicitly and harden into protocol. The client owns its retry policy.
- **Idempotency as a call option or client option.** A gRPC `CallOption` or
  HTTP `CallOption` reaches only one transport's call surface and cannot be
  read by middleware; a client-wide option declares too much. The context
  marker is transport-neutral, per-call, and readable by any middleware.
- **A closed set of named backoff curves** (an enum such as
  `BackoffExponential | BackoffLinear | BackoffConstant`, selectable from
  config). Configurable from YAML, but every curve not in the set is
  unreachable, and each addition is a framework change plus a config schema
  change. The interface costs one method and admits curves the framework
  never has to enumerate. An enum can still be layered on later as a config
  convenience that constructs a `Backoff`, without changing the seam.
- **`Backoff` as a plain `func(attempt int) time.Duration`.** Equivalent for
  stateless curves and lighter to write, but a curve with state — a
  decorrelated jitter carrying its previous wait — becomes a closure over
  mutable variables with no place to document its concurrency contract. The
  named interface gives implementations a receiver and a doc comment.
- **Hedged requests.** Sending a second attempt before the first fails
  trades load for latency and requires idempotency unconditionally plus
  cancellation plumbing. Out of scope for a conservative default; nothing
  in the policy shape precludes a separate hedging middleware later.
