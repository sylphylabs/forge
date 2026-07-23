# OpenAPI 3.2 Generation

Status: Phase 1 implemented and schema-validated; advanced 3.2 features pending

## Decision

OpenKratos provides `protoc-gen-openapi` as a fourth atomic build-time
plugin in the `github.com/openkratos/kratos/cmd` module. It derives an
OpenAPI document from protobuf descriptors and `google.api.HttpRule`; it does
not inspect registered runtime handlers or generated Go source.

The plugin is based on the Apache-2.0 Google gnostic generator at `v0.7.1`, so existing
`gnostic.openapi.v3` document, operation, schema, and property annotations
continue to work. OpenKratos owns the fork because its HTTP error envelope and
transcoding behavior differ from gnostic's grpc-gateway defaults.

## Phase 1 Contract

The default output declares `openapi: 3.2.0` and supports the OpenAPI subset
needed by current OpenKratos unary HTTP transcoding:

- protobuf request and response schemas;
- Google HTTP paths, query parameters, bodies, and additional bindings;
- existing gnostic OpenAPI v3 annotations;
- merged or source-relative YAML output;
- deterministic component references;
- the OpenKratos HTTP JSON error envelope.

The canonical HTTP error schema is `openkratos.errors.v1.Status`:

```json
{
  "code": 404,
  "reason": "USER_NOT_FOUND",
  "message": "user not found",
  "metadata": {"resource": "users/123"}
}
```

`reason`, `message`, and `metadata` remain optional in the schema because
the default JSON codec omits protobuf zero values. The generator does not claim
that an absent optional field is present on the wire.

This is not `google.rpc.Status`. The gRPC transport projects an OpenKratos
error into `google.rpc.Status` plus `google.rpc.ErrorInfo`; OpenAPI describes
the HTTP/JSON surface and therefore references the four-field OpenKratos
schema.

When `default_response=true`, every operation receives a `default` response
referencing that schema. Independently of the default response option,
explicitly annotated `4xx` and `5xx` responses with no content automatically
receive `application/json` content referencing the same schema. Explicit
response content is never overwritten.

## Options

The plugin accepts the inherited gnostic options plus OpenKratos-specific
defaults:

| Option | Default | Meaning |
| --- | --- | --- |
| `openapi_version` | `3.2.0` | OpenAPI version string emitted in the document |
| `version` | `0.0.1` | API information version |
| `naming` | `json` | JSON or protobuf field names |
| `fq_schema_naming` | `false` | Prefix message schemas with the proto package |
| `enum_type` | `integer` | Integer or string enum representation |
| `depth` | `2` | Recursive query-message expansion depth |
| `default_response` | `true` | Add the shared OpenKratos default error response |
| `error_schema_name` | `openkratos.errors.v1.Status` | Error component name |
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
`default_response=false` if they want the shared OpenKratos default response.
Existing explicit error responses can remain; the plugin fills missing content
schemas automatically.

Generated fixtures are parsed independently with `libopenapi` and validated
with `jsonschema/v6` against libopenapi's embedded official OpenAPI 3.2 schema.
These dependencies are confined to the generator module and do not enter the
OpenKratos runtime module.

## Deliberate Limitations

Declaring version 3.2 does not mean every new OAS 3.2 object is expressible
from protobuf annotations. Phase 1 intentionally does not claim support for:

- the OAS 3.2 `QUERY` Path Item operation;
- sequential or streaming media types for streaming RPCs;
- hierarchical tags and other 3.2-only document metadata;
- validation-annotation projection into JSON Schema constraints;
- external service configuration parity with `protoc-gen-go-http`;
- automatic method-to-error-reason discovery.

Unsupported features must be added with explicit fixtures and diagnostics;
changing only the version string is not sufficient evidence of support.

## Next Gates

1. Share normalized HTTP binding analysis with `protoc-gen-go-http` so paths,
   bodies, response projection, and custom verbs cannot drift.
2. Add a method-level error declaration that links RPC methods to generated
   error enum values, then emit exact status/reason response documentation.
3. Project supported Protovalidate and field-behavior annotations into JSON
   Schema constraints.
4. Design explicit `QUERY` and streaming behavior before enabling either.
