# OpenAPI 3.2 Generation

Status: Core generation, shared HTTP binding semantics, Problem Details
schema alignment, method error declarations, and `sylphy.openapi.v1`
annotations implemented; advanced 3.2 features pending

## Decision

Forge provides `protoc-gen-openapi` as a fourth atomic build-time
plugin in the `github.com/sylphylabs/forge/cmd` module. It derives an
OpenAPI document from protobuf descriptors and `google.api.HttpRule`; it does
not inspect registered runtime handlers or generated Go source.

The generator builds documents on its own write-only model
(`cmd/internal/openapi/model`): plain Go structs covering the OpenAPI 3.2
object set, serialized deterministically to YAML through an explicitly
ordered node tree. Every keyed collection is an ordered slice of named
entries, so equal documents marshal to equal bytes without relying on
encoder map ordering. The model can express the full 3.2 surface — the
`query` path item operation, `additionalOperations`, sequential media type
fields (`itemSchema`, `prefixEncoding`, `itemEncoding`), hierarchical tags
(`parent`, `kind`, `summary`), and `$self` — independently of what the
generator currently emits. The model does no parsing and no OAS schema
validation; generated fixtures are validated on the test side (see Options).
Rationale and rejected alternatives are in
[ADR-0015](../adr/0015-openapi-write-only-model-and-slim-annotations.md).

## Phase 1 Contract

The default output declares `openapi: 3.2.0` and supports the OpenAPI subset
needed by current Forge unary HTTP transcoding:

- protobuf request and response schemas;
- Google HTTP paths, query parameters, bodies, and additional bindings;
- scalar, repeated, map, message, and whole-message request bodies;
- `response_body` field projection and `google.api.HttpBody` media types;
- `sylphy.openapi.v1` presentation annotations;
- merged or source-relative YAML output;
- deterministic component references and byte-deterministic output;
- the Forge RFC 9457 Problem Details error representation.

The canonical HTTP error component is a generator-owned `ForgeProblem`, not a
Protobuf message. It describes exactly the document the runtime error encoder
writes — the wire contract in [`errors.md`](errors.md):

```json
{
  "kind": "NOT_FOUND",
  "domain": "sylphy.user.v1",
  "reason": "USER_FAILURE_REASON_NOT_FOUND",
  "message": "user not found",
  "metadata": {"resource": "users/123"},
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "violations": [{"field": "user.email", "description": "malformed"}]
}
```

`kind` is required on Forge-produced errors. Complete domain/reason identity,
message, metadata, trace ID, and violations are optional and omitted when
empty. The media type is RFC 9457's `application/problem+json`, but the members
are the Forge errors contract's own vocabulary; RFC 9457's `type`, `title`,
`status`, and `detail` members are deliberately not part of the contract, for
the reasons given in [`errors.md`](errors.md). The component is synthesized
from that stable framework contract, not discovered from a generated
descriptor, and a contract test in `protoc-gen-openapi` asserts the schema's
property set equals the key set the published runtime encoder actually writes.

This is not `google.rpc.Status`. The public Protobuf API intentionally defines
no Status envelope. gRPC projects an error into `google.rpc.Status` plus native
details; OpenAPI describes only the HTTP Problem Details surface.

When `default_response=true`, every operation receives a `default` response
referencing that schema.

## Annotations

`sylphy.openapi.v1` (in the Forge API module,
`api/proto/sylphy/openapi/v1/annotations.proto`) carries only what protobuf
descriptors cannot already say: presentation metadata, server URLs, and
security. Schemas come from messages, paths from `google.api.http`, and error
responses from `sylphy.errors.v1` throws declarations — none of those have an
annotation, so the document cannot disagree with the contract it was
generated from. The vocabulary grows append-only; a field is added when a
need is proven.

```proto
import "sylphy/openapi/v1/annotations.proto";

option (sylphy.openapi.v1.document) = {
  title: "Library API"
  version: "1.2.3"
  description: "Manages books and shelves."
  servers: {url: "https://api.example.com"}
  security_schemes: {
    name: "bearer"
    http_bearer: {bearer_format: "JWT"}
  }
  security_schemes: {
    name: "api_key"
    api_key_header: {header: "X-Api-Key"}
  }
};

message GetBookRequest {
  option (sylphy.openapi.v1.schema) = {description: "Request to fetch one book."};

  string name = 1 [(sylphy.openapi.v1.field) = {
    description: "Resource name of the book."
    example: "shelves/1/books/2"
  }];
}

service LibraryService {
  rpc GetBook(GetBookRequest) returns (Book) {
    option (google.api.http) = {get: "/v1/{name=shelves/*/books/*}"};
    option (sylphy.openapi.v1.operation) = {
      summary: "Get one book"
      tags: "books"
      security: {schemes: "bearer"}
    };
  }
}
```

- `document` (FileOptions, 500301): `title`, `version`, and `description`
  override the corresponding plugin options; `servers` become the document
  server list, ahead of any `google.api.default_host` derivation;
  `security_schemes` define the named schemes under
  `components.securitySchemes`. In merged output the annotations of all
  generated files combine — scalars last-writer-wins in file order, servers
  and schemes accumulate; defining the same scheme name twice fails
  generation.
- `operation` (MethodOptions, 500302): `summary`, `description` (overrides
  the method comment), `tags` (replaces the default service-name tag),
  `deprecated`, and `security` — a list of requirements with OR-across,
  AND-within semantics. A requirement naming a scheme no `document`
  annotation defines fails generation with a diagnostic. There is no
  responses field: error responses come from throws declarations only.
- `schema` (MessageOptions, 500303): `description` overrides the message
  comment.
- `field` (FieldOptions, 500304): `description` overrides the field comment;
  `example` is emitted as the property example; `format` overrides the
  derived format.

Security schemes cover HTTP bearer and API key in a header; other forms are
added append-only when needed. The plugin resolves the annotations
dynamically — by full name against the request's own descriptors, the same
mechanism as the throws marker — so it works without linking the generated
annotation types.

Explicitly documented error responses beyond the default come exclusively
from method error declarations, below.

## Method Error Declarations

Methods document their exact error responses from declarations, not
handwritten status-code literals. The Forge API defines one marker:

```proto
extend google.protobuf.FieldOptions {
  bool throws = 500103;
}
```

An application extends `google.protobuf.MethodOptions` — and, for errors every
method of a service can raise, `google.protobuf.ServiceOptions` — with a
repeated field of its own error reason enum and sets the marker on that
extension field:

```proto
extend google.protobuf.MethodOptions {
  repeated ShelfFailureReason throws = 50000 [(sylphy.errors.v1.throws) = true];
}
extend google.protobuf.ServiceOptions {
  repeated ShelfFailureReason service_throws = 50001 [(sylphy.errors.v1.throws) = true];
}

service ShelfService {
  option (service_throws) = SHELF_FAILURE_REASON_DENIED;
  rpc GetShelf(GetShelfRequest) returns (Shelf) {
    option (throws) = SHELF_FAILURE_REASON_NOT_FOUND;
  }
}
```

The field type is the application's own enum, so the compiler rejects a reason
that does not exist. The generator claims only extension fields carrying the
marker — an extension that merely happens to be typed by an error enum is
never interpreted as a declaration — and resolves the marker dynamically from
the request's descriptors, so it works against any application package.

For each method, the union of service-level and method-level declarations is
resolved to Kinds (the value-level `kind` annotation, falling back to the
enum-level `default_kind`), projected onto HTTP status codes through the same
projection the runtime error encoder uses, and grouped into one response per
status code. Each response carries `application/problem+json` content
referencing the shared error component and a description listing every
identity behind the code, one line per `(kind, domain)` pair:

```yaml
"403":
  description: 'PERMISSION_DENIED (sylphy.shelf.v1) — reasons: SHELF_FAILURE_REASON_DENIED'
```

When a method's request message carries any `buf.validate` constraint
(message, field, or oneof level, discovered recursively with cycle
protection), the framework identity `forge.sylphylabs.io / VALIDATION_FAILED`
is merged into that method's `400` response alongside any declared 400
reasons; a method with no declarations at all still receives the `400`. The
`validation_reason` option disables this.

Declared responses coexist with the `default` response, which keeps its
catch-all semantics. Generation fails, with a diagnostic naming the offense,
when:

- the marker is set on a field that is not an extension of `MethodOptions` or
  `ServiceOptions`;
- a marked extension field is not a repeated enum;
- a declaration references the enum zero value;
- a declared value resolves to no kind (no value-level `kind`, no enum-level
  `default_kind`);
- a kind projects to a status outside 4xx/5xx;
- the same identity is declared more than once for one method (including
  through the service-level union);
- a declaration-produced status code collides with a response already present
  on the operation — the declaration is the single source.

Extensions the descriptor set cannot resolve are ignored; every resolvable
marked declaration is discovered.

## Options

The plugin accepts these options:

| Option | Default | Meaning |
| --- | --- | --- |
| `openapi_version` | `3.2.0` | OpenAPI version string emitted in the document |
| `version` | `0.0.1` | API information version |
| `naming` | `json` | JSON or protobuf field names |
| `fq_schema_naming` | `false` | Prefix message schemas with the proto package |
| `depth` | `2` | Recursive query-message expansion depth |
| `default_response` | `true` | Add the shared Forge default error response |
| `error_schema_name` | `ForgeProblem` | Error component name |
| `output_mode` | `merged` | One merged document or source-relative documents |
| `validation_reason` | `true` | Document `VALIDATION_FAILED` on methods whose request carries `buf.validate` constraints |

Example Buf configuration:

```yaml
plugins:
  - local: protoc-gen-openapi
    strategy: all
    out: openapi
    opt:
      - naming=proto
      - fq_schema_naming=true
      - output_mode=merged
```

Enum representation is not an option. Forge pins protojson without
`UseEnumNumbers`, and the form codec that binds query and path values resolves
an enum by name before it falls back to a number, so an enum crosses the wire
as its value name in every direction. An enum field is therefore documented as
a `string` listing every declared value name, the same way a 64-bit integer is
documented as a `string` because that is what protojson writes.

Generated fixtures are parsed independently with `libopenapi` and validated
with `jsonschema/v6` against libopenapi's embedded official OpenAPI 3.2 schema.
These dependencies are confined to the generator module's tests and enter
neither the generation path nor the Forge runtime module.

## Shared HTTP Binding Semantics

`protoc-gen-openapi` and `protoc-gen-go-http` use the same normalized HTTP
binding analyzer. OpenAPI generation therefore fails on the same invalid
contracts that runtime code generation rejects, including:

- invalid path, body, query, or `response_body` fields;
- nested `additional_bindings`;
- duplicate match sets and structurally conflicting routes.

The OpenAPI generator emits the standard path-item methods: `GET`, `PUT`,
`POST`, `DELETE`, `OPTIONS`, `HEAD`, `PATCH`, and `TRACE`. A custom
`google.api.HttpRule` using one of those method names is supported. `QUERY`
and arbitrary custom method names fail generation with an explicit diagnostic
instead of being silently omitted.

## Deliberate Limitations

Declaring version 3.2 does not mean every new OAS 3.2 object is emitted from
protobuf annotations. The document model expresses all of the following; the
generator's emit logic intentionally does not produce them yet:

- the OAS 3.2 `QUERY` path item operation (`PathItem.Query` in the model);
- arbitrary custom HTTP operations (`PathItem.AdditionalOperations`);
- sequential media types for streaming RPCs (`MediaType.ItemSchema`,
  `PrefixEncoding`, `ItemEncoding`);
- hierarchical tags (`Tag.Parent`, `Kind`, `Summary`) and `$self`;
- validation-annotation projection into JSON Schema constraints;
- external service configuration parity with `protoc-gen-go-http`.

Unsupported features must be added with explicit fixtures and diagnostics;
changing only the version string is not sufficient evidence of support.

## Next Gates

1. Diff generated OpenAPI documents in CI, so a declaration that drifts from
   behavior fails a gate instead of aging into fiction. Method-level
   declarations are implemented
   ([ADR-0013](../adr/0013-method-error-declaration-via-marked-extensions.md)),
   and generated wrappers assert at runtime that an outbound reason is in the
   method's declared set
   ([ADR-0014](../adr/0014-runtime-throws-assertion-in-generated-wrappers.md)).
2. Project supported Protovalidate and field-behavior annotations into JSON
   Schema constraints.
3. Emit `QUERY`, `additionalOperations`, and sequential media types for
   streaming RPCs. The model already expresses them
   ([ADR-0015](../adr/0015-openapi-write-only-model-and-slim-annotations.md));
   what remains is generator emit logic, fixtures, and diagnostics.
