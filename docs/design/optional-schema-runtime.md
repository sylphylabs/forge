# Optional Schema Runtime

Status: implemented

Last reviewed: August 10, 2026

## Purpose

This document defines how Protobuf reaches Forge's HTTP transport without being
required by it: the `transport/http/transcoding` package, the seam it installs
itself into, and the rule that decides what belongs on each side.

It is written for two readers. An application author needs to know when to
import the subpackage. A contributor adding a schema-aware capability needs to
know where it goes and why the seam has the shape it has.

## Decision

`transport/http` speaks bytes and Go values. Everything that needs a declared
schema — binding a path variable onto a message field, choosing an encoder that
spells a field the way its schema declares it, decoding one frame into a named
sub-message — lives in `transport/http/transcoding`.

Importing the subpackage installs it:

```go
import _ "github.com/sylphylabs/forge/transport/http/transcoding"
```

Generated bindings import it on the application's behalf, so a service built
from `.proto` files needs no extra declaration. A hand-written service that
binds Protobuf messages imports it directly.

### Why a package boundary

A service that serves plain JSON does not link the Protobuf runtime:

| Application | Binary | Protobuf packages |
| --- | --- | --- |
| Plain JSON over `transport/http` | 11 MB | 0 |
| Generated bindings | 18 MB | 40 |

The linker drops a package nothing references. A build tag would compile two
different transports and double the test matrix; a runtime flag would not remove
the import at all. A package boundary is the only mechanism in Go that produces
this result, which is why the rule in
[elegance §4b](../../../docs/explanation/framework-elegance-criteria.md) requires
one for any dependency claimed to be optional.

### The seam

`transport/http/schema.go` declares one interface:

```go
type Schema interface {
    Owns(v any) bool

    BindPath(v any, vars []PathVar) error
    BindValues(v any, values url.Values) error
    Codec(negotiated encoding.Codec, v any) encoding.Codec
    Target(v any) any
    RawBody(v any) (RawBody, bool)
    DecodeField(v any, field string, read func(target any) error) error
}
```

**Ownership is asked once.** Every operation begins with the same question —
does this runtime understand this value? — so `Owns` asks it, and the remaining
methods assume the answer was yes. The transport treats anything unowned as a
plain Go value.

That shape is deliberate. The first implementation was a struct of six function
fields, each reporting whether it had handled the value, and each deciding
independently: one tested `v.(proto.Message)`, another inspected the codec's
name, a third tested for the raw-body type. Three ways to answer one question,
and a fourth would have arrived with the next capability. Collapsing the
question into `Owns` means a new capability adds a method that may assume
ownership rather than another way to ask about it.

The call sites show the difference:

```go
// Before: every site checks whether the hook exists and whether it applied.
if schema.BindValues != nil {
    if handled, err := schema.BindValues(values, target); handled {
        return err
    }
}
return formDecoder.Decode(target, values)

// After: one question, then a decision.
if schemaOwns(target) {
    return schema.BindValues(target, values)
}
return formDecoder.Decode(target, values)
```

`schemaOwns` also folds "no runtime linked" and "not my value" into one case, so
neither is repeated at six call sites.

### What belongs on each side

A capability belongs in `transcoding` when it cannot be answered without a
declared schema:

| Capability | Why it needs the schema |
| --- | --- |
| `BindPath`, `BindValues` | Resolving a name to a field requires the field set |
| `Codec` | Only the schema knows a field's wire spelling |
| `Target` | Allocating a nil message target requires its type |
| `RawBody` | The raw-payload carrier is a generated type |
| `DecodeField` | Locating a named sub-message requires the descriptor |

Everything else — routing, middleware, headers, status codes, the Problem
document — stays in the transport, because none of it needs a declaration.

`Target` earns its place by having been lost once. Collapsing the original
`DecodeBody` hook dropped the allocation of a pointer-to-message-pointer, and a
test caught it immediately. The logic had been buried in a reflection branch
where no reader would find it; naming it as a method makes the responsibility
visible.

### Codec selection follows the target

A message is encoded by its schema even when the request negotiated
`application/json`:

```go
func (runtime) Codec(negotiated encoding.Codec, v any) encoding.Codec {
    if negotiated.Name() != "json" {
        return negotiated
    }
    ...protojson
}
```

`encoding/json` cannot read or write a message. A Duration, a Timestamp, any
well-known type, and an int64 spelled as a string all differ between their Go
and JSON forms, and `encoding/json` drops them *silently* — it sees well-formed
JSON and a struct that happens not to match, reports no error, and leaves the
field zero. Routing a message to ProtoJSON makes `application/json` mean what
the message's schema says it means.

Only the standard JSON codec is replaced. Registering any other codec is a
deliberate act: a service that installs its own `application/x-thrift` means it,
and this runtime has no standing to override that choice. Two existing tests
enforce that boundary.

## Consequences

**A hand-written service that binds Protobuf messages must import the
subpackage.** Without it, a raw `HttpBody` is not recognized, a path variable is
not bound onto a message field, and a stream body field reports that the schema
runtime is missing. The transport says so rather than failing silently.

**`transport` no longer blank-imports the Protobuf codecs.** `transcoding`
registers them, because it is what needs them. A service that wants `proto` or
`protojson` content types without generated code imports the subpackage for the
same reason.

**A test that exercises schema behaviour must link the runtime.** Because
`transcoding` imports `transport/http`, an internal test cannot import it back;
such tests live in `package http_test`.

## Rationale and rejected alternatives

### Six function fields

The original seam. Rejected once a fourth capability was needed: each field
carried its own ownership test, so the same question was answered three
different ways and every new capability added a fourth. An interface with one
`Owns` method holds the question in one place.

### A capability interface per operation

The repository's established pattern for optional behaviour is a single-method
interface plus a type assertion, as with `transport.Healthzer` and
`transport.GracefulStopper`. It is right there because each capability is
genuinely independent: a server may drain without reporting readiness.

Here the capabilities are not independent — they all apply to exactly the values
`Owns` accepts, and a runtime that implemented `BindPath` but not `Codec` would
produce a message bound from its path and then encoded as a Go struct. One
interface makes that incoherence unrepresentable.

### Build tags or a runtime flag

A build tag compiles two transports with different semantics and doubles the
test matrix. A runtime flag does not remove the import, so the dependency is
linked whether or not it is used. Neither achieves the goal, which is that a
plain-JSON service does not carry the Protobuf runtime.
