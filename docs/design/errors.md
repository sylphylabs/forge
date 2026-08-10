# Errors

Status: accepted; implementation alignment in progress

Last reviewed: August 10, 2026

> **Implementation status.** This document describes what the code does.
> `docs/agent/errors.md` covers the same API for a reader who only wants to use
> it.

## Purpose

This document defines Forge's error contract: the local Go value, the public
data that may cross a process boundary, the generated sentinel API, and the
HTTP and gRPC projections. It is the design authority for error semantics.
`docs/agent/errors.md` describes only the API that is currently implemented and
must not get ahead of the code.

Logging is out of scope. An error records what failed and preserves its local
cause. Middleware decides whether to log, count, or trace that failure.

## Decision

Forge separates four concerns that used to be represented by one generated
message:

| Concern | Answers | Owner |
| --- | --- | --- |
| Classification | What class of failure is this? | `errors.Kind` |
| Identity | Which stable failure is this, and who owns it? | domain + reason |
| Public explanation | What may a remote caller receive? | `errors.Public` |
| Local diagnosis | What actually caused it here? | Go wrapper chain |

HTTP status codes, gRPC codes, JSON objects, and Protobuf messages are
projections. None is stored in the core error value and none is the source of
truth for another transport.

### Package ownership

The target dependency graph is:

```text
errors
  Kind, Error, Public, Violation, identity and trace helpers
  standard library only; no JSON or Protobuf implementation

transport/http
  generic net/http client/server, Problem Details, JSON, SSE and WebSocket

transport/http/transcoding
  proto.Message, ProtoJSON, binary Protobuf, google.api.HttpBody,
  Google HTTP path/query binding, response-body projection, stream fields

transport/grpc
  canonical code mapping, status projection, ErrorInfo, BadRequest, TraceInfo

api/errors/v1
  Kind annotations and the small TraceInfo gRPC detail; no Status envelope
```

`errors/protobuf` does not exist in the target design. A transport-neutral
error has no canonical Protobuf envelope. HTTP uses Problem Details and gRPC
uses the standard status model plus details native to that protocol.

The root `transport` package imports no codec for side effects. Codec selection
belongs to each HTTP client or server instance. Generated HTTP code explicitly
imports `transport/http/transcoding`; importing generic `transport/http` alone
must not link any Protobuf package.

## Core model

### Kind

`Kind` is a small, closed semantic vocabulary:

```go
const (
    KindUnknown Kind = iota
    KindInvalidArgument
    KindFailedPrecondition
    KindOutOfRange
    KindUnauthenticated
    KindPermissionDenied
    KindNotFound
    KindAlreadyExists
    KindConflict
    KindResourceExhausted
    KindCanceled
    KindDeadlineExceeded
    KindUnavailable
    KindUnimplemented
    KindInternal
    KindDataLoss
)
```

`Kind` does not answer whether an operation is safe to retry. Retry safety also
depends on whether the attempt was sent, whether execution is ambiguous, and
whether the operation is idempotent. `Kind.Retryable` and
`errors.IsRetryable` therefore do not exist. The retry contract is defined in
`docs/design/retry.md`.

### Identity and matching

A stable identity is a pair of non-empty strings: domain + reason. Generated
errors use the Protobuf package as the domain and the full enum value name as
the reason.

`Error.Is` matches only when both errors have complete identities and the two
pairs are equal. Kind and message never participate. In particular, two
anonymous `KindNotFound` errors do not match merely because their Kinds are the
same. Callers that care only about classification use `KindOf`.

The standard library checks direct pointer equality before calling `Is`, so an
anonymous sentinel still matches itself. A derived anonymous error has no
stable cross-instance identity and is intentionally matched by Kind instead.

### Public by construction

`Error` stores public contract data separately from its local cause. A
cause-free owned snapshot is the only input accepted by a transport:

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

`PublicOf` clones maps and slices and never includes a cause, a formatted error
string, a stack, or an arbitrary wrapped value. For a plain Go error it returns
`KindUnknown`, no message, and any explicitly attached trace ID. A transport
supplies its own generic title or status message; it never publishes
`err.Error()` by accident.

Calling `Msg`, `Meta`, `WithMetadata`, or adding a `Violation` is an explicit
declaration that the value is part of the caller-visible contract. Secrets,
queries, internal addresses, and raw dependency text belong in a wrapped cause
or in logs, not in those fields.

This structural boundary replaces `PolicySafe`, `PolicyStrict`, and
`PolicyVerbose`. A Kind cannot prove where a message came from, and a mutable
process-global policy cannot prove that arbitrary metadata is safe. Forge does
not claim to infer that information. An application that needs a different
external representation supplies a custom transport encoder.

The policy model this replaced demonstrated why, measured on 2026-08-10
`[一手数据]`:

```
KindNotFound + metadata{"dsn": "postgres://user:hunter2@..."}  -> sent verbatim
violation description carrying raw "pq: duplicate key ..."      -> sent verbatim
PolicySafe = PolicyVerbose                                      -> redaction off
```

A policy reads the Kind and guesses; it never inspects metadata or violation
text, and the three policies are writable exported package variables that any
dependency can reassign. Declaring public data is something only the caller can
do correctly.

`FromPublic` creates a remote Error with no cause. Accessors return owned data.
The transport decoder accepts an identity only when domain and reason are both
present; a partial identity is treated as anonymous.

### Construction

Contract errors are immutable sentinels:

```go
var ErrFailureReasonNotFound = errors.MustDefine(
    errors.KindNotFound,
    "sylphy.document.v1",
    "FAILURE_REASON_NOT_FOUND",
)

return ErrFailureReasonNotFound.
    Msgf("document %q not found", id).
    Meta("tenant", tenantID).
    Wrap(cause)
```

Every deriving method returns a copy with independently owned maps and slices.
`Wrap(nil)` is a no-op. `Define` is for declaration-time constants and rejects
an empty or malformed identity. Anonymous local failures use `New(kind)`; there
are no setters that turn an anonymous error into a partially identified one.

`Error()` may include the local cause because it is for local Go diagnostics.
No transport serializes `Error()`.

Contract identity and trace fields are bounded so every transport can preserve
the mandatory set:

| Field | Contract |
| --- | --- |
| domain | 1-255 ASCII bytes; dot-separated identifier segments matching Protobuf package syntax |
| reason | 1-128 ASCII bytes matching `[A-Z][A-Z0-9_]*` |
| trace ID | empty or exactly 32 lowercase hexadecimal characters |

`Define` rejects an invalid domain/reason at declaration time, and the error
generator reports it with file and enum context. `WithTraceID` ignores an
invalid trace ID. Transport readers discard an invalid identity or trace rather
than creating a value that cannot be forwarded safely. Public messages,
metadata, and violations must be valid UTF-8; transports apply their own total
payload budget to those optional fields.

### Trace attachment

Tracing must work for every error, including a plain error that has not yet
been classified by a transport. It also must not replace the wrapper chain.

```go
func WithTraceID(err error, traceID string) error
func TraceIDOf(err error) string
```

`WithTraceID` returns a transparent wrapper whose `Unwrap` is the exact input
error. `errors.Is` and `errors.As` therefore retain the complete chain. It is a
no-op for nil, an empty ID, or an error that already carries a trace. A remote
trace is never overwritten by the receiving service's trace.

`PublicOf` overlays the attached trace onto the public snapshot. This lets the
OpenTelemetry middleware attach a trace before the transport converts a plain
error to `KindUnknown`.

### Violations

Validation aggregation is explicit:

```go
var v errors.Violations
v.Add("email", "malformed")
v.Addf("age", "must be positive, got %d", age)
return v.Err(errors.KindInvalidArgument)
```

`Err` returns nil when the collection is empty. A violation description is
public contract data. `errors.Join` remains for local aggregation; transport
projection selects one primary Forge error and does not pretend that an
arbitrary error tree is one remote status.

## Declaring contract errors

Error identities are declared next to the service contract:

```proto
import "sylphy/errors/v1/errors.proto";

enum FailureReason {
  option (sylphy.errors.v1.default_kind) = KIND_INTERNAL;

  FAILURE_REASON_UNSPECIFIED = 0;
  FAILURE_REASON_NOT_FOUND = 1 [(sylphy.errors.v1.kind) = KIND_NOT_FOUND];
  FAILURE_REASON_BACKEND_DOWN = 2;
}
```

An enum participates when it declares `default_kind` or any non-zero value
declares `kind`. Once it participates, every non-zero sibling generates a
sentinel; unannotated values inherit `default_kind`, or `KIND_INTERNAL` when no
enum default is present. An unannotated enum remains an ordinary enum.

Generated Go names always include the enum name:

```go
var ErrFailureReasonNotFound = errors.MustDefine(...)
var ErrFailureReasonBackendDown = errors.MustDefine(...)
```

Keeping the enum namespace prevents two enums in one Go package from both
emitting `ErrNotFound`. Names never change depending on whether a collision
happens to exist today.

The generator rejects a kind on the zero value, `KIND_UNSPECIFIED` on an error,
a reason outside `SCREAMING_SNAKE_CASE`, a reason without its enum prefix, and
any duplicate generated Go identifier. Generation is all-or-nothing for each
file; it never silently skips an error value from a participating enum.

## Service layering

The meaning of a failure is decided in exactly one place:

| Layer | Does | Does not |
| --- | --- | --- |
| data | wraps infrastructure errors with `%w` | names a public domain error |
| business | translates a foreign sentinel once and wraps its cause | returns an unclassified dependency error |
| service | returns the business error unchanged | translates it again or logs it |
| transport | projects `PublicOf(err)` | invents business identity |

An unexpected dependency error is normally wrapped by an anonymous
`KindInternal` error. It remains locally inspectable, but only fields the
business layer explicitly made public can leave the process.

## HTTP projection

HTTP errors have one representation regardless of the successful response
codec:

```http
Content-Type: application/problem+json
```

The media type is RFC 9457's, so a proxy or a browser recognizes the body as an
error document. The members are Forge's own:

```json
{
  "kind": "NOT_FOUND",
  "domain": "sylphy.document.v1",
  "reason": "FAILURE_REASON_NOT_FOUND",
  "message": "document \"42\" not found",
  "metadata": {"tenant": "t1"},
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736"
}
```

Empty members are omitted. The error encoder ignores `Accept`; changing the
success representation to XML, YAML, ProtoJSON, or binary Protobuf does not
change the error contract.

RFC 9457's `type`, `title`, `detail`, and `status` members are deliberately not
emitted. `title` and `detail` are prose for a human reader, and this contract
already carries `kind` and `reason` for a program plus `message` for a person —
adding them would mean two overlapping vocabularies for one value, and readers
would have to guess which is authoritative. `type` would be the constant
`about:blank` and carries no information.

`status` is omitted for a stronger reason: repeating the status line inside the
body creates the very contradiction rule 4 exists to reject. The status line is
authoritative, so a second copy can only ever agree redundantly or disagree
harmfully.

### HTTP reader rules

The response status line is authoritative. A client follows these rules in
order:

1. A 2xx response is not an error.
2. The default classification comes from the actual HTTP status.
3. A body is decoded only when its media type is
   `application/problem+json` (parameters are allowed), it is non-empty, it is
   at most 64 KiB, and it contains exactly one JSON object.
4. A present Problem `status` must equal the actual status. A recognized
   `kind` must project to that status. Either conflict invalidates the semantic
   body and returns the status-only fallback, so a stale proxy body cannot make
   a 503 match a NotFound sentinel.
5. A missing or future unknown `kind` uses the status-derived Kind while
   retaining otherwise valid public fields. Unknown JSON members are ignored.
6. Domain and reason are retained only as a complete pair. Empty, malformed,
   oversized, or partial identities do not match sentinels.
7. An empty body, malformed JSON, an unsupported content type, or an oversized
   body returns the status-only fallback. A zero-value decoded object is never
   accepted as a successful Forge error.

These rules are both tolerant and conservative: additive fields and future Kind
names do not destroy useful detail, while contradictory evidence cannot create
a false stable identity.

Each rule closes a defect that was measured before it existed `[一手数据]`,
2026-08-10:

```
503 response + stale "kind":"NOT_FOUND" body -> matched the NotFound sentinel
                                                (a caller stopped retrying a
                                                transient failure)
503 response + text/html body                -> parsed as a Forge error
                                                (an nginx page, a WAF block)
8 MiB message field                          -> accepted whole
                                                (unbounded memory from an
                                                untrusted peer)
```

The body limit is `MaxProblemBytes`, 64 KiB.

Terminal SSE and WebSocket failures carry the same Problem JSON object, not the
selected stream message codec. Stream contexts retain request cancellation,
client disconnect, and server shutdown. A timeout intended only for unary calls
must be applied by unary middleware; stream code never uses
`context.WithoutCancel` to erase lifecycle signals.

## gRPC projection

The gRPC status code is the Kind projection and its message is the public
message. Details are protocol-native:

| Detail | Carries |
| --- | --- |
| `google.rpc.ErrorInfo` | complete domain/reason identity and public metadata |
| `google.rpc.BadRequest` | public field violations |
| `sylphy.errors.v1.TraceInfo` | trace ID and whether optional details were truncated |

Trace IDs are not request IDs and are never encoded as
`google.rpc.RequestInfo.request_id`.

The serialized `google.rpc.Status` has a hard 4096-byte budget. Projection is
deterministic:

1. Reserve the code/Kind, a complete identity without metadata, and TraceInfo.
2. Retain the public message, truncating it at a UTF-8 boundary only when the
   essential status would otherwise exceed the budget.
3. Add violations in declaration order while they fit.
4. Add metadata in sorted-key order while it fits.
5. Set `TraceInfo.details_truncated` when any public message bytes, violation,
   or metadata entry are omitted.

Identity and trace have bounded core representations, so the mandatory set
always fits. Optional diagnostics never turn a classified application failure
into a transport failure at the default 8 KiB gRPC header/trailer boundary.

On receipt, the gRPC code is authoritative for Kind. Known details restore the
public snapshot; unknown details are ignored. The converted error continues to
implement `GRPCStatus`, while `errors.Is` and `errors.As` reach the reconstructed
Forge error.

Conversion happens before application middleware observes an error. This is
required for unary calls, stream establishment, `SendMsg`, and `RecvMsg`.
Middleware therefore sees the same Forge classification and identity on every
gRPC call shape, and callers can still use grpc-go's status APIs.

## HTTP Protobuf boundary

`transport/http` defaults to an instance-owned JSON codec set. There is no
mutable package registry and no blank-import registration. Adding a codec to
one client or server cannot change another instance.

`transport/http/transcoding` owns every reference to `proto.Message`,
`protoreflect`, ProtoJSON, binary Protobuf, Google HTTP annotations,
`google.api.HttpBody`, query/path reflection, response-body projection, and
stream-field binding. Generated `_http.pb.go` files import that package and call
it directly for:

- precompiled client paths and query expansion;
- request body, path, and query binding;
- ProtoJSON whole-message and field projections;
- `HttpBody` raw payloads;
- Protobuf-aware SSE and WebSocket streams.

The core HTTP package exposes only small consumer-side codec and stream hooks;
it does not switch on codec names such as `proto` or inspect Protobuf values.
The acceptance test is dependency closure, not directory layout:

```text
go list -deps ./transport/http -> protobuf=0, grpc=0
```

## Retry boundary

No transport status or Kind proves that a non-idempotent operation did not run.
In particular, gRPC `Unavailable` also represents a connection lost after bytes
were sent. Transports mark only errors for which they know the request was not
sent, using the transport-level wrapper described in `docs/design/retry.md`.

All other retry decisions belong to the retry middleware and require the
operation's idempotency declaration where execution may be ambiguous.

## Evolution rules

- Kind names and domain/reason identities are stable contract values.
- Readers ignore unknown Problem extensions and gRPC details.
- An unknown HTTP Kind falls back to status classification; it does not reject
  an otherwise valid Problem.
- An unknown gRPC code becomes `KindUnknown`; known details may still be kept.
- Cause chains and arbitrary Go values never cross a process boundary.
- Removing or changing a published identity is a service-contract change.
- The Protobuf annotation schema is versioned independently from HTTP Problem
  JSON and gRPC status details.

## Rationale and rejected alternatives

### Kind instead of a transport status

HTTP has far more status codes than gRPC. The earlier HTTP -> gRPC -> HTTP
round-trip collapsed common values such as 422 to 500. A closed semantic Kind
with one-way projections is total and avoids that loss.

### No core JSON or Protobuf envelope

Implementing `json.Unmarshaler` on `*Error` lets decoding mutate a shared
generated sentinel. A core JSON shape also makes a transport format part of the
domain package. Both are avoided when HTTP owns a separate Problem DTO and
decoding constructs a new Error from `Public`.

A generated Protobuf `Status` envelope was rejected for the same ownership
reason. gRPC already has a status envelope and standard details; HTTP has
Problem Details. A third supposedly canonical envelope only forces both
transports through an unrelated representation.

### Problem media type without Problem prose members

Using `application/problem+json` is worth it: proxies, browsers, and API
tooling recognize the media type and stop guessing. Emitting RFC 9457's `type`,
`title`, `detail`, and `status` members is not.

`title` and `detail` exist because RFC 9457 has no machine-readable identity of
its own — they are the whole payload for a reader who has nothing better. This
contract does have one: `kind` classifies, `reason` identifies, `message`
explains. Adding `title` and `detail` beside them creates two vocabularies for
one value and makes every reader choose which to trust.

`status` is worse than redundant. Rule 4 rejects a body whose classification
contradicts the status line, because a stale intermediary can serve an old body
under a new status. A `status` member inside that body is a second copy of the
same fact, subject to the same staleness, and adding it would mean the document
can contradict itself as well as its response.

### No Kind-based disclosure policy

`KindNotFound` does not prove that its message or metadata is safe, and
`KindInternal` does not prove that an explicitly authored message is unsafe.
The former policy model guessed provenance it could not observe. Public fields
plus a structurally excluded cause form a real information boundary.

### Enum-qualified generated names

Dropping the enum prefix made generated names pleasant only while a package had
one error enum. Adding another enum could make the package stop compiling or
force an existing identifier to change. Always including the enum name is less
magical and permanently collision-free.

### No global codec registry

Process-global registration makes imports configure unrelated clients and
servers, introduces initialization-order behavior, and makes Protobuf linkage
implicit. Instance-owned codec sets make configuration visible, concurrent,
and independently testable.
