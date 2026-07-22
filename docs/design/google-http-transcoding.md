# Google HTTP Transcoding Conformance

Status: Phase 1 implemented and validated; release packaging pending; Phase 2 scope only

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
- Omitted `response_body` and top-level named response fields. The Google
  contract uses omission for the whole response; `response_body: "*"` is
  invalid.
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

## Implementation Status

Phase 1 now has one shared path parser for generation, client expansion,
ServeMux registration, and escaped-path extraction. Invalid descriptors and
conflicting bindings fail generation without partial output. Generated unary
clients and servers use `application/json` with descriptor-aware protobuf JSON
adapters for whole messages and projected fields.

Real Edition 2023 Open and Opaque fixtures compile and execute generated code.
They cover primary client/server requests, multiple additional bindings,
concrete and unspecified custom methods, bare wildcards, `%2F`, named and whole
bodies, nested and repeated query parameters, and message/scalar/repeated/map
response projections. Focused runtime tests cover enum, bytes, 64-bit integers,
non-finite floats, repeated messages, path-field omission, and preservation of
unrelated projected fields.

The remaining release task is packaging, not Phase 1 behavior: the nested
generator module's repository-relative `replace` must be removed after a
matching root version exists, then a versioned `go install` smoke test must pass.

External service config and `fully_decode_reserved_expansion` remain unsupported
and belong exclusively to Phase 2.

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

The initial Go API is concrete and intentionally small:

```go
type SyntaxError struct {
	Offset int
	Reason string
}

type Variable struct {
	FieldPath string
	Template  string
	Multi     bool
}

func Parse(pattern string) (*Template, error)
func (t *Template) Pattern() string
func (t *Template) ServeMuxPattern() string
func (t *Template) MatchKey() string
func (t *Template) HasUnboundWildcard() bool
func (t *Template) Variables() []Variable
func (t *Template) Expand(resolve func(fieldPath string) (string, error)) (string, error)
func (t *Template) Extract(escapedPath string) (map[string]string, error)
```

`MatchKey` describes the decoded path match set while ignoring protobuf field
names, so generators can reject equivalent bindings. `Variables` returns a
copy. `Expand` and `Extract` return wrapped errors with template and variable
context. The resolver supplies canonical string values; the parser package does
not import protobuf reflection.

Do not add an interface for the parser. There is one implementation and all
operations are deterministic pure functions or methods on the parsed template.

The nested generator module may import this internal package because its module
path is within `github.com/openkratos/kratos`. During pre-release development,
its `go.mod` requires the root module at `v0.0.0` and uses a repository-relative
replacement. The root module does not depend on the generator, so this does not
create a module cycle.

The replacement is local-development scaffolding, not a publishable dependency
contract. Before a generator tag is released, remove the replacement and require
the matching released root-module version. `go install module@version` must be
validated against the tagged module because installable modules cannot rely on a
repository-relative replacement.

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
- A bare `*` or `**` segment that is not owned by a path variable and therefore
  has no request field from which a generated client can obtain a value.
- An invalid template.

The current `BuildPath` API cannot report these failures. Before the first
OpenKratos release, replace it with:

```go
func BuildPath(pathTemplate string, msg proto.Message, opts ...BuildPathOption) (string, error)
```

Generated clients propagate the error before invoking the HTTP client. A nil
message is an error when the template or query projection references request
fields. This is an intentional pre-v1 source break; do not retain silent
fallback behavior in a second compatibility helper.

Path and query scalar text follows protobuf JSON lexical rules: enum names,
base64 bytes, decimal numeric values, `true`/`false`, and the protobuf JSON
spellings for non-finite floats. Both client projection and server binding use
the same descriptor-aware conversion; Go debug formatting is not a wire format.

For `custom.kind = "*"`, server registration remains method-unspecified. There
is no canonical method for a generated client to send. Add an exported
`transport/http.ErrUnspecifiedHTTPMethod` sentinel and make the generated client
method return it before network I/O. Callers use `errors.Is`; the client must
never send `*` as an HTTP request method. A service that needs a generated client
must declare a concrete primary rule.

Bare path wildcards have the same client-side ambiguity. Servers and raw HTTP
additional-binding fixtures support them, but a generated primary client returns
`transport/http.ErrUnboundPathWildcard` before network I/O.

### Descriptor Validation

`protoc-gen-go-http` validates every rule before rendering source. An error
includes the fully qualified RPC name, rule path, and offending field path.

Generation fails when:

- A path variable is missing from the request message.
- Any component before the leaf is not a singular message.
- The leaf path field is repeated, a map, or a message/group.
- `body` is neither `*` nor a top-level existing field.
- `response_body` is non-empty and is not a top-level existing field;
  `response_body: "*"` is rejected.
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

Named body and response-body fields are top-level fields, as required by the
vendored contract. They may be repeated or maps: `HttpRule` explicitly permits
array request/response bodies through repeated fields, and protobuf maps use the
corresponding JSON object representation.

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
- `response_body: "*"` fails generation; omit `response_body` to encode the
  whole response.
- A wildcard custom method makes its generated client return
  `ErrUnspecifiedHTTPMethod` before network I/O.
- A primary rule with a bare path wildcard makes its generated client return
  `ErrUnboundPathWildcard` before network I/O.

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
| Response body | Omitted, invalid `*`, message, scalar, enum, int64/uint64, repeated, map, `HttpBody` |
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
- The released generator module has no repository-relative `replace` directive,
  requires the matching root release, and passes a versioned `go install` smoke
  test.
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
