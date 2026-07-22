# Google HTTP Transcoding Conformance

Status: Phase 1 accepted design, implementation pending; Phase 2 scope only

Last reviewed: July 22, 2026

## Purpose

This document is the implementation contract for making OpenKratos conform to
the Google HTTP transcoding model defined by `google.api.HttpRule`. It separates
behavior that is part of the Google contract from Gorilla mux compatibility and
from OpenKratos streaming extensions.

Current compatibility facts remain in [`COMPATIBILITY.md`](../../COMPATIBILITY.md).
Nothing in this document becomes a compatibility claim until its acceptance
tests pass and that document is updated.

The vendored [`google/api/http.proto`](../../third_party/google/api/http.proto)
is the normative source for path grammar, field classification, body projection,
and additional bindings. Generated Go comments, grpc-gateway behavior, and other
transcoders are comparison evidence, not substitutes for that contract.

## Goals

- Fully support inline `(google.api.http)` annotations for unary RPCs.
- Use one parsed path-template representation for generation, server matching,
  request variable extraction, and generated client expansion.
- Preserve Google percent-encoding semantics, including encoded slashes in
  multi-segment variables.
- Apply protobuf JSON rules to whole messages and projected fields.
- Reject invalid rules during code generation with method and field context.
- Validate the primary generated client and all registered server bindings
  through executable requests for both Open and Opaque protobuf APIs.
- Keep `net/http.ServeMux` as the routing tree. The conformance layer owns the
  Google semantics that ServeMux does not provide.

## Non-goals

- Restoring arbitrary Gorilla mux regular expressions or registration-order
  precedence.
- Treating non-terminal `**` as valid. Google requires `**` to terminate the
  path except for a custom verb.
- Making SSE or WebSocket behavior part of Google unary HTTP conformance.
- Claiming that every grpc-gateway extension is part of `google.api.HttpRule`.
- Loading remote configuration or schemas during code generation.
- Optimizing the conformance code before correctness and allocation profiles
  identify a material request-path cost.

## Scope

Work is split into two independently releasable phases.

### Phase 1: Inline HttpRule Conformance

Phase 1 covers rules attached directly to protobuf methods:

- `get`, `put`, `post`, `delete`, and `patch` patterns.
- `custom` methods, including method-unspecified `kind: "*"` server rules.
- Literal segments, `*`, terminal `**`, field paths, composite resource-name
  variables, and terminal custom verbs.
- No body, whole-message `body: "*"`, and top-level named body fields.
- Omitted `response_body`, `response_body: "*"`, and top-level named response
  fields.
- One level of `additional_bindings`. Nested additional bindings are invalid.
- Path, body, and query field classification and precedence.
- `google.api.HttpBody` for whole messages and projected fields.

Phase 1 is the release gate for claiming inline `HttpRule` conformance.

### Phase 2: Google API Service Config

Phase 2 covers external `google.api.Service` YAML configuration:

- HTTP rule selection by fully qualified RPC selector.
- Service-config rules overriding matching inline annotations.
- `fully_decode_reserved_expansion`.
- A deterministic generator option for a local service-config file.

Phase 2 must not begin by widening Phase 1 APIs speculatively. Its CLI and merge
contract require a separate approved design after Phase 1 lands.

## Current Gaps

The current implementation supports the common path grammar, generated route
registration, named bodies, named response bodies, and one-level additional
bindings. It does not yet satisfy the complete contract:

1. `http.Request.PathValue` returns decoded wildcard values. Google requires
   `%2F` and `%2f` to remain encoded by default inside multi-segment variables.
2. `transport/http` routing, `BuildPath`, and `protoc-gen-go-http` each parse
   templates independently. Their accepted grammar and escaping behavior can
   diverge.
3. Repeated and map path fields currently produce warnings rather than hard
   generator errors. A path variable that terminates at a message field is not
   rejected consistently.
4. Projected scalar, repeated, map, and enum bodies use ordinary Go JSON in
   some generated paths. That is incorrect for protobuf-specific cases such as
   64-bit integers, enum names, and non-finite floating-point values.
5. The server can represent `custom.kind = "*"`, but a generated client cannot
   infer one concrete HTTP method from an unspecified-method rule.
6. `additional_bindings` has generator logic but no complete generated-client
   and registered-server round-trip fixture.
7. External service config and `fully_decode_reserved_expansion` are not
   supported.

## Design

### One Template Parser

Add a small, dependency-free `internal/httprule` package in the root module. It
owns only Google path-template syntax and percent-encoding rules. It performs no
network, filesystem, protobuf reflection, or HTTP response work.

The parser returns an immutable template containing literal, wildcard, variable,
and custom-verb segments. Consumers use that representation instead of regular
expressions over the original template string.

The package must support these operations:

- Parse and validate a template with byte-positioned errors.
- Expose referenced protobuf field paths and their variable subtemplates.
- Produce the structural ServeMux pattern used by the server adapter.
- Expand a template from field values using Google escaping rules.
- Recover variable values from `URL.EscapedPath`, preserving the distinction
  between structural `/` and encoded `%2F`.

Do not add an interface for the parser. There is one implementation and all
operations are deterministic pure functions or methods on the parsed template.

The nested generator module may import this internal package because its module
path is within `github.com/openkratos/kratos`. Its `go.mod` will require the root
module at the matching release version and use a repository-relative replacement
for local development. The root module does not depend on the generator, so this
does not create a module cycle.

### Server Matching And Extraction

ServeMux remains responsible for method dispatch, structural path matching,
precedence, redirects, and conflict detection. A route bucket retains the parsed
Google template selected for that handler.

After ServeMux selects the route, OpenKratos extracts public variables from
`Request.URL.EscapedPath()` with the parsed template. It must not reconstruct a
multi-segment resource name only from decoded `PathValue` values, because that
loses the difference between `/` and `%2F`.

The following default behavior is required:

```text
template: /v1/{name=publishers/*/books/*}
request:  /v1/publishers/a%2Fb/books/1
name:     publishers/a%2Fb/books/1
```

Single-segment variables perform full reverse decoding. Multi-segment variables
reverse-decode non-reserved characters but preserve `%2F` or `%2f` exactly as
received. Matching never captures the leading `/`.

`Context.Vars()` and `Request.PathValue(fieldPath)` must return the same public
value. Internal ServeMux wildcard names remain inaccessible and continue using
the reserved OpenKratos prefix.

Malformed request-target escapes are rejected with HTTP 400 before application
middleware runs. A capture-layout mismatch after ServeMux selected a route is an
internal invariant failure: return HTTP 500 through the configured error encoder,
do not invoke the application handler, and do not retry with decoded values or a
different route.

### Generated Client Expansion

Generated clients use the same parsed grammar and escaping rules as the server.
Single-segment expansion percent-encodes every character except
`[-_.~0-9a-zA-Z]`. Multi-segment expansion also leaves structural `/` unescaped
while encoding `?`, `#`, and percent signs correctly.

Path expansion must return an error for:

- A missing or invalid field path.
- A value that does not satisfy its literal and wildcard resource-name shape.
- A repeated, map, or terminal message field used as a path variable.
- An invalid template.

The current `BuildPath` API cannot report these failures. Before the first
OpenKratos release, change it to return `(string, error)` and update generated
clients to propagate the error. This is an intentional pre-v1 source break; do
not retain silent fallback behavior in a second compatibility helper.

For `custom.kind = "*"`, server registration remains method-unspecified. There
is no canonical method for a generated client to send. Add an exported
`transport/http.ErrUnspecifiedHTTPMethod` sentinel and make the generated client
method return it before network I/O. Callers use `errors.Is`; the client must
never send `*` as an HTTP request method. A service that needs a generated client
must declare a concrete primary rule.

### Descriptor Validation

`protoc-gen-go-http` validates every rule before rendering source. An error
includes the fully qualified RPC name, rule path, and offending field path.

Generation fails when:

- A path variable is missing from the request message.
- Any component before the leaf is not a singular message.
- The leaf path field is repeated, a map, or a message/group.
- `body` or `response_body` is neither `*` nor a top-level existing field.
- A nested additional binding contains another additional binding.
- A custom method is empty, or its path is empty.
- Two generated rules have equivalent method and path match sets.
- The path template violates the Google grammar.

Warnings are not sufficient for invalid Google rules. Parser and descriptor
helpers return wrapped errors rather than calling `os.Exit`. The generator entry
point reports the error through `protogen` and emits no partial file for the
invalid input.

### Request Field Precedence

Generated handlers populate one request message in this order:

1. HTTP body.
2. Query parameters.
3. Path variables.

The sources are contractually disjoint for a valid rule. The order ensures the
path remains authoritative if an invalid client duplicates a field. For
`body: "*"`, no query fields are accepted; path fields are still applied after
the body.

Generated clients classify request leaf fields exactly once:

- Path-referenced fields are emitted only in the URL path.
- The named body field and all of its descendants are emitted only in the body.
- With `body: "*"`, all non-path fields are emitted only in the body.
- Remaining supported leaves are emitted as query parameters using protobuf
  JSON field names.
- Repeated scalar query fields use repeated query keys.
- Repeated message fields are rejected as query parameters.

### ProtoJSON Field Projection

Whole-message and projected-field JSON must follow protobuf JSON semantics.
Ordinary `encoding/json` is insufficient for descriptor-sensitive values.

Add descriptor-aware helpers that marshal or unmarshal a top-level protobuf
field while retaining its parent message descriptor. They must cover:

- Singular scalar and enum fields.
- Singular message fields.
- Repeated scalar, enum, and message fields where the Google contract permits
  an array body.
- Map fields where the Google contract permits an object body.
- Bytes, 64-bit integers, enum names, well-known types, and non-finite floats.
- Open and Opaque Go protobuf APIs without direct struct-field assumptions.

The helpers return errors and never silently switch from ProtoJSON to ordinary
JSON. Google-transcoded JSON uses `Content-Type: application/json` and ProtoJSON
wire semantics without changing the general-purpose `encoding/json` codec.
Generated handlers call the descriptor-aware projection helpers directly;
`google.api.HttpBody` retains its declared media type. The existing
`application/protojson` codec remains available for explicit OpenKratos calls but
is not the public media type of a Google-transcoded endpoint.

### Additional Bindings

The primary rule and every additional binding produce separate server route
descriptors for the same RPC implementation. One level is supported, matching
the Google contract. Nested additional bindings are rejected.

Registration order must not change which legal binding matches. Equivalent or
conflicting bindings fail generation instead of relying on route order.

The generated Go client uses the primary rule only. Additional bindings are
alternative REST entry points for external clients and do not create additional
Go methods or call options. Their conformance tests send raw `net/http` requests
to the registered server and assert that they invoke the same RPC implementation.
This is an explicit API decision, not an accidental generator limitation.

## Compatibility

Phase 1 intentionally changes observable behavior before v1:

- Multi-segment values containing encoded slash retain `%2F` instead of becoming
  an additional structural slash.
- Invalid path-field and body declarations that previously warned or failed at
  runtime now fail code generation.
- Projected protobuf fields use ProtoJSON wire representations.
- `BuildPath` returns an error.
- A wildcard custom method makes its generated client return
  `ErrUnspecifiedHTTPMethod` before network I/O.

These changes require English and Simplified Chinese migration entries. They are
not optional compatibility modes because two decoding or JSON rules would make
authorization and signature behavior ambiguous.

## Security Requirements

- Authorization, middleware route metadata, and protobuf binding must observe
  the same extracted path values.
- Encoded slash handling must be tested against path-confusion and prefix-bypass
  cases.
- Malformed percent escapes fail closed.
- No parser accepts a leading slash as part of a variable value.
- Generated code does not read service configuration from the network.
- Error messages may include descriptor and template names but never request
  body contents or runtime credentials.

## Conformance Matrix

Phase 1 fixtures must cover at least:

| Area | Required cases |
| --- | --- |
| Methods | Five standard methods, custom method, unspecified `*` server method |
| Templates | Root, literals, `{x}`, `{x=*}`, composite resource names, empty and non-empty terminal `**`, custom verbs |
| Escaping | Unicode, space, `%`, `%2F`, `%2f`, `?`, `#`, encoded literals, malformed escapes |
| Fields | Nested scalar path, missing field, repeated/map/message leaf rejection, invalid intermediate field |
| Body | Omitted, `*`, message, scalar, enum, bytes, repeated, map, `HttpBody` |
| Response body | Omitted, `*`, message, scalar, enum, int64/uint64, repeated, map, `HttpBody` |
| Query | Nested leaves, repeated scalar, path/body omission, repeated-message rejection, field-mask behavior |
| Bindings | Primary plus multiple additional bindings, nested-binding rejection, duplicate/conflict rejection |
| Protobuf API | Edition 2023 Open and Opaque generated code |
| Round trip | Primary generated client round trip; raw HTTP requests for every additional binding |

Golden generated text is supporting evidence only. Acceptance requires compiled
generated code and executable request/response assertions.

## Implementation Sequence

1. Add `internal/httprule` parsing, expansion, extraction, and table-driven tests.
2. Move server route compilation and public variable extraction to the shared
   representation, including raw escaped-path conformance.
3. Change `BuildPath` to return errors and migrate generated clients.
4. Add strict generator descriptor and rule validation.
5. Add descriptor-aware body and response-body projection.
6. Add executable additional-binding and custom-method fixtures.
7. Run the full conformance matrix for Open and Opaque APIs.
8. Update compatibility and migration documents only after behavior is validated.
9. Design and implement Phase 2 service-config loading separately.

Each step should be an isolated commit with focused tests. Performance evidence,
if needed, is a separate commit from semantic conformance changes.

## Acceptance Gate

Phase 1 is complete only when all of the following hold:

- Every matrix row has an executable positive and negative fixture.
- Root HTTP tests and generator tests pass with race detection where shared
  runtime state is involved.
- Real `protoc` generation compiles and runs for Open and Opaque APIs.
- All repository Go modules compile after the `BuildPath` API change.
- `go vet` passes in the root and generator modules.
- The primary generated client/server round trip proves `%2F`, ProtoJSON
  projected fields, and concrete custom methods on the wire; raw HTTP fixtures
  prove every additional and unspecified-method server binding.
- `COMPATIBILITY.md`, `COMPATIBILITY_zh.md`, and both migration guides describe
  the validated behavior and source breaks.
- No document claims external Service Config support until Phase 2 passes its
  own acceptance gate.

Minimum validation commands include:

```shell
go test ./internal/httprule ./transport/http
go test -race ./transport/http
go test ./...
go vet ./...

cd cmd/protoc-gen-go-http
go test ./...
go vet ./...
```

The repository-wide multi-module compile check documented in `DEVELOPMENT.md`
must also pass. External services are not required for these conformance tests.
