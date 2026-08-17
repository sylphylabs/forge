# OpenAPI 3.2 Generation

Status: Core generation, shared HTTP binding semantics, Problem Details
schema alignment, and method error declarations implemented; advanced 3.2
features pending

## Decision

Forge provides `protoc-gen-openapi` as a fourth atomic build-time
plugin in the `github.com/sylphylabs/forge/cmd` module. It derives an
OpenAPI document from protobuf descriptors and `google.api.HttpRule`; it does
not inspect registered runtime handlers or generated Go source.

The plugin is based on the Apache-2.0 Google gnostic generator at `v0.7.1`, so existing
`gnostic.openapi.v3` document, operation, schema, and property annotations
continue to work. Forge owns the fork because its HTTP error envelope and
transcoding behavior differ from gnostic's grpc-gateway defaults.

## Phase 1 Contract

The default output declares `openapi: 3.2.0` and supports the OpenAPI subset
needed by current Forge unary HTTP transcoding:

- protobuf request and response schemas;
- Google HTTP paths, query parameters, bodies, and additional bindings;
- scalar, repeated, map, message, and whole-message request bodies;
- `response_body` field projection and `google.api.HttpBody` media types;
- existing gnostic OpenAPI v3 annotations;
- merged or source-relative YAML output;
- deterministic component references;
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
referencing that schema. Independently of the default response option,
explicitly annotated `4xx` and `5xx` responses with no content automatically
receive `application/problem+json` content referencing the same schema. Explicit
response content is never overwritten.

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
- a declaration-produced status code collides with a handwritten gnostic
  response literal on the same method — the declaration is the single source,
  the literal must go.

Extensions the descriptor set cannot resolve are ignored; every resolvable
marked declaration is discovered.

## Options

The plugin accepts the inherited gnostic options plus Forge-specific
defaults:

| Option | Default | Meaning |
| --- | --- | --- |
| `openapi_version` | `3.2.0` | OpenAPI version string emitted in the document |
| `version` | `0.0.1` | API information version |
| `naming` | `json` | JSON or protobuf field names |
| `fq_schema_naming` | `false` | Prefix message schemas with the proto package |
| `enum_type` | `integer` | Integer or string enum representation |
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
      - enum_type=string
      - fq_schema_naming=true
      - output_mode=merged
```

Applications migrating from Google's gnostic plugin should remove
`default_response=false` if they want the shared Forge default response.
Existing explicit error responses can remain; the plugin fills missing content
schemas automatically.

Generated fixtures are parsed independently with `libopenapi` and validated
with `jsonschema/v6` against libopenapi's embedded official OpenAPI 3.2 schema.
These dependencies are confined to the generator module and do not enter the
Forge runtime module.

## Shared HTTP Binding Semantics

`protoc-gen-openapi` and `protoc-gen-go-http` use the same normalized HTTP
binding analyzer. OpenAPI generation therefore fails on the same invalid
contracts that runtime code generation rejects, including:

- invalid path, body, query, or `response_body` fields;
- nested `additional_bindings`;
- duplicate match sets and structurally conflicting routes.

The OpenAPI generator emits all HTTP methods represented by the current
gnostic path-item model: `GET`, `PUT`, `POST`, `DELETE`, `OPTIONS`, `HEAD`,
`PATCH`, and `TRACE`. A custom `google.api.HttpRule` using one of those method
names is supported. `QUERY` and arbitrary custom method names fail generation
with an explicit diagnostic instead of being silently omitted.

## Deliberate Limitations

Declaring version 3.2 does not mean every new OAS 3.2 object is expressible
from protobuf annotations. Phase 1 intentionally does not claim support for:

- the OAS 3.2 `QUERY` Path Item operation, because the current gnostic model
  does not expose it;
- arbitrary custom HTTP operations outside the standard path-item methods;
- sequential or streaming media types for streaming RPCs;
- hierarchical tags and other 3.2-only document metadata;
- validation-annotation projection into JSON Schema constraints;
- external service configuration parity with `protoc-gen-go-http`.

Unsupported features must be added with explicit fixtures and diagnostics;
changing only the version string is not sufficient evidence of support.

## Next Gates

1. Enforce the declarations at runtime — assert an outbound reason is in the
   method's declared set — and diff generated OpenAPI documents in CI, so a
   declaration that drifts from behavior fails a gate instead of aging into
   fiction. Method-level declarations themselves are implemented; see
   [ADR-0013](../adr/0013-method-error-declaration-via-marked-extensions.md).
2. Project supported Protovalidate and field-behavior annotations into JSON
   Schema constraints.
3. Replace or extend the gnostic model before enabling `QUERY`, arbitrary
   operations, or streaming behavior.
