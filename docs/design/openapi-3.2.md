# OpenAPI 3.2 Generation

Status: Core generation and shared HTTP binding semantics implemented;
Problem Details schema alignment and advanced 3.2 features pending

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
Protobuf message:

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "user not found",
  "kind": "NOT_FOUND",
  "domain": "sylphy.user.v1",
  "reason": "USER_FAILURE_REASON_NOT_FOUND",
  "metadata": {"resource": "users/123"},
  "trace_id": "4bf92f3577b34da6a3ce929d0e0e4736",
  "violations": []
}
```

`type`, `title`, `status`, and `kind` are required on Forge-produced errors.
`detail`, complete domain/reason identity, metadata, trace ID, and violations
are optional and omitted when empty. The component follows the wire contract in
[`errors.md`](errors.md); it is synthesized from that stable framework
contract, not discovered from a generated `Status` descriptor.

This is not `google.rpc.Status` and it is not
`sylphy.errors.v1.Status`. The public Protobuf API intentionally defines no
Status envelope. gRPC projects an error into `google.rpc.Status` plus native
details; OpenAPI describes only the HTTP Problem Details surface.

When `default_response=true`, every operation receives a `default` response
referencing that schema. Independently of the default response option,
explicitly annotated `4xx` and `5xx` responses with no content automatically
receive `application/problem+json` content referencing the same schema. Explicit
response content is never overwritten.

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
- external service configuration parity with `protoc-gen-go-http`;
- automatic method-to-error-reason discovery.

Unsupported features must be added with explicit fixtures and diagnostics;
changing only the version string is not sufficient evidence of support.

## Next Gates

1. Add a method-level error declaration that links RPC methods to generated
   error enum values, then emit exact status/reason response documentation.
2. Project supported Protovalidate and field-behavior annotations into JSON
   Schema constraints.
3. Replace or extend the gnostic model before enabling `QUERY`, arbitrary
   operations, or streaming behavior.
