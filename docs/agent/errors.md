# Errors

Package `github.com/sylphylabs/forge/errors`.

An error carries a **Kind** (what class of failure), an **identity** (domain +
reason), and a **cause** (why). It never carries a status code: HTTP and gRPC
statuses are projected from the Kind at the transport boundary.

For the reasoning behind this design, read
[docs/design/errors.md](../design/errors.md). This guide is the usage contract.

## Kind

The vocabulary is closed. These are all of them.

| Kind | Meaning | HTTP |
| --- | --- | --- |
| `KindUnknown` | unclassified; the zero value | 500 |
| `KindInvalidArgument` | caller supplied a malformed argument | 400 |
| `KindFailedPrecondition` | system not in the state the call requires | 412 |
| `KindOutOfRange` | argument outside the valid range | 422 |
| `KindUnauthenticated` | caller could not be identified | 401 |
| `KindPermissionDenied` | caller known but not allowed | 403 |
| `KindNotFound` | requested entity does not exist | 404 |
| `KindAlreadyExists` | entity to create is already present | 409 |
| `KindConflict` | a concurrent change prevented completion | 409 |
| `KindResourceExhausted` | quota or rate limit reached | 429 |
| `KindCanceled` | caller went away | 499 |
| `KindDeadlineExceeded` | call outlived its deadline | 504 |
| `KindUnavailable` | dependency temporarily unreachable | 503 |
| `KindUnimplemented` | operation not supported | 501 |
| `KindInternal` | an invariant broke; a bug | 500 |
| `KindDataLoss` | data lost or irrecoverably corrupted | 500 |

Match on Kind rather than comparing status codes. A Kind says what went
wrong; it does not say whether retrying is safe, which also depends on
whether the request reached a server and whether the operation is idempotent.
Retry policy lives in `middleware/retry`.

## Declaring contract errors in Protobuf

An error that is part of a service's public contract is declared in the `.proto`
that owns the contract. Annotate the enum, never a status code:

```proto
import "sylphy/errors/v1/errors.proto";

enum FailureReason {
  option (sylphy.errors.v1.default_kind) = KIND_INTERNAL;

  FAILURE_REASON_UNSPECIFIED = 0;
  FAILURE_REASON_NOT_FOUND = 1 [(sylphy.errors.v1.kind) = KIND_NOT_FOUND];
}
```

Values without their own `kind` inherit `default_kind`; without a `default_kind`
they are `KIND_INTERNAL`. An enum that declares neither annotation is an
ordinary enum and generates nothing.

`protoc-gen-go-errors` emits one immutable sentinel per value:

```go
var ErrNotFound = errors.MustDefine(
	errors.KindNotFound,
	"sylphy.test.v1",
	"FAILURE_REASON_NOT_FOUND",
)
```

The generator rejects at build time: a reason that is not
`SCREAMING_SNAKE_CASE`, a reason not prefixed by its enum name, and a
`_UNSPECIFIED = 0` value used as an error.

## Returning an error

Sentinels are immutable. Every method returns a copy, so deriving from a shared
sentinel is safe across goroutines.

```go
return v1.ErrNotFound.
	Msgf("document %q", name).
	Meta("tenant", tenantID).
	Wrap(cause)
```

- `Msg` / `Msgf` set the human-readable message.
- `Wrap(cause)` records the underlying error. Wrapping `nil` is a no-op, so you
  need not branch. Prefer it over folding `cause.Error()` into the message: the
  message is for humans, the cause is for `errors.Is` and `errors.As`.
- `Meta(k, v)` / `WithMetadata(map)` attach structured context.

For a failure that never leaves the process, skip Protobuf:

```go
return errors.Of(errors.KindInternal).WithReason("CACHE_CORRUPT").Wrap(err)
```

Do not return a bare non-Forge `err` from a business layer. It arrives at the
boundary as `KindUnknown`, projects to 500, and carries nothing to grep for.

## Inspecting an error

Forge adds no matching vocabulary. Use the standard library.

```go
if errors.Is(err, v1.ErrNotFound) { ... }

var e *errors.Error
if errors.As(err, &e) { ... }
```

`Is` matches on domain and reason only — deliberately not the message, so two
occurrences of the same failure with different interpolated messages still
match. When only the class matters:

```go
switch errors.KindOf(err) {
case errors.KindNotFound:    // ...
case errors.KindUnavailable: // ...
}
```

`KindOf`, `ReasonOf`, `DomainOf`, and `FromError` accept any error, return
zero values for `nil`, and never panic — including on a typed-nil `*Error`.
They do not import transport vocabularies: Forge's HTTP and gRPC clients convert
remote responses before application middleware receives them. A foreign error
that did not pass through a Forge transport classifies as `KindUnknown`.

## Aggregating field failures

A validation pass reports everything it found, not the first thing:

```go
var v errors.Violations
v.Add("email", "malformed")
v.Addf("age", "must be positive, got %d", age)
return v.Err(errors.KindInvalidArgument)
```

`Err` returns `nil` when nothing was recorded, so returning it unconditionally
is correct. Violations survive the RPC boundary as `errdetails.BadRequest`.

`errors.Join` is **not** aggregation here: a joined error projects only the
first error's kind and reason onto the wire and silently drops the rest. Use it
only for collecting independent failures for local logging.

## Layering

The rule: **the meaning of a failure is decided in exactly one place.** If the
same reason is constructed in two layers, one of them is wrong.

| Layer | Does | Does not |
| --- | --- | --- |
| data / storage | wraps with `%w` | name a domain error |
| business | translates foreign sentinels into contract errors, wraps causes | return a bare `err` with no kind |
| service / handler | returns the error unchanged | translate again, or log |
| transport | projects onto the wire | anything you write by hand |

Storage does not get to decide that `sql.ErrNoRows` means "not found" to a user
— that is a business judgement, and if storage made it, swapping the store would
rewrite the service's error contract.

## Crossing a process boundary

Kind, domain, reason, metadata, trace ID, and violations all survive, so a
remote error matches its generated sentinel exactly as a local one does:

```go
resp, err := client.GetDocument(ctx, req)
if err != nil {
	if errors.Is(err, remotev1.ErrNotFound) { ... }
	if errors.KindOf(err) == errors.KindUnavailable { ... }
	return nil, err
}
```

**The cause chain does not cross the boundary.** A cause routinely holds a
connection string or a query fragment; sending it would publish that to every
caller. `Unwrap` returns `nil` on a received error and `errors.As` will not
reach into the callee. Correlate across services by trace ID — the tracing
middleware stamps the ambient trace onto outgoing errors
(see [observability.md](observability.md)). An error that already names a trace
keeps it, and a remote error is never re-stamped.

Over HTTP the body names the kind rather than numbering it:

```json
{
  "kind": "NOT_FOUND",
  "domain": "sylphy.test.v1",
  "reason": "FAILURE_REASON_NOT_FOUND",
  "message": "document \"42\" not found",
  "trace_id": "a1b2c3"
}
```

## Choosing what to disclose

What an outgoing error reveals is decided by construction, not by
configuration. A transport serializes only `errors.PublicOf(err)` — the kind,
the domain/reason identity, and the message, metadata, and violations the
caller explicitly set. The cause chain and any wrapped Go value are excluded
structurally; no option sends them.

```go
type Public struct {
	Kind       Kind
	Domain     string
	Reason     string
	Message    string
	Metadata   map[string]string
	TraceID    string
	Violations []Violation
}

func PublicOf(err error) Public
func FromPublic(p Public) *Error
```

Calling `Msg`, `Meta`, `WithMetadata`, or adding a `Violation` is the
declaration that a value is public. Secrets, queries, and raw dependency text
belong in a wrapped cause or in logs — never in those fields, because those
fields cross the boundary verbatim.

A non-Forge error discloses only `KindUnknown`: its text was written for an
operator, so the transport substitutes its own generic message rather than
publishing `err.Error()`. `FromPublic` is the receiving side — it rebuilds a
remote error, with no cause, from the same set of facts.

There is no per-deployment redaction knob. An application that needs a
different external representation supplies a custom transport encoder.

## Never write these

| Wrong | Right |
| --- | --- |
| `errors.New(404, "NOT_FOUND", "msg")` | `errors.Of(errors.KindNotFound).Msg("msg")` |
| `errors.Newf(...)` / `errors.Errorf(...)` | `errors.Of(kind).Msgf(...)` |
| `err.WithCause(cause)` | `err.Wrap(cause)` |
| `errors.IsNotFound(err)` | `errors.Is(err, v1.ErrSomething)` or `errors.KindOf(err)` |
| `errors.Code(err)` / `err.Code` | `errors.KindOf(err)`; the status is a projection |
| `errors.Join` to report field failures | `errors.Violations` |
| a `kind` annotation on a status code in `.proto` | annotate with `KIND_*` values only |
