# OpenKratos Protobuf Generation

Status: approved implementation contract

Last reviewed: July 23, 2026

## Purpose

This document defines the ownership, executable topology, output boundaries,
release model, and migration rules for OpenKratos Protobuf generation.

The public annotation schemas are defined by
[`public-protobuf-api-module.md`](public-protobuf-api-module.md). Generated
middleware plans are defined by
[`generated-middleware.md`](generated-middleware.md). Google HTTP transcoding
behavior is defined by
[`google-http-transcoding.md`](google-http-transcoding.md).

## Decision

OpenKratos owns one Go code-generation plugin:

```text
github.com/openkratos/kratos/cmd/protoc-gen-go-openkratos
```

The executable name is `protoc-gen-go-openkratos`, and its `protoc` flag is
`--go-openkratos_out`.

This plugin replaces the separate OpenKratos-owned HTTP and error generators.
It also owns generated middleware plans, shared operation facts, and
OpenKratos transport wrappers as those contracts are implemented.

The consolidation boundary is ownership, not all Protobuf generation:

| Plugin | Owner | Responsibility |
| --- | --- | --- |
| `protoc-gen-go` | Google Protobuf | Go messages, enums, descriptors, and extensions |
| `protoc-gen-go-grpc` | grpc-go | Standard gRPC clients, servers, stream interfaces, and service descriptors |
| Validation or documentation plugins | Their upstream projects | Independent generated contracts selected by an application |
| `protoc-gen-go-openkratos` | OpenKratos | Error helpers, middleware plans, operation facts, HTTP bindings, and OpenKratos transport wrappers |

OpenKratos must not copy `protoc-gen-go` or `protoc-gen-go-grpc` merely to offer
one command. Buf or `protoc` remains the orchestrator for independently owned
plugins.

## One Executable, Separate Passes

The plugin is one release artifact and receives one
`CodeGeneratorRequest`. Its implementation remains split into focused passes:

```text
cmd/protoc-gen-go-openkratos/
|-- main.go
`-- internal/generator/
    |-- errors/
    |-- middleware/
    |-- operation/
    |-- http/
    `-- grpcadapter/
```

The exact package layout may stay flatter while a pass is small. The required
boundary is that errors, operation analysis, middleware wiring, HTTP wire
generation, and gRPC adaptation do not become one template or one mutable
cross-transport model. Shared descriptor analysis has one owner, and output
passes consume its validated result.

One executable does not imply one generated source file. Outputs stay separate
so ownership, diffs, and compile failures remain local:

| Output | Contents | Emission condition |
| --- | --- | --- |
| `<prefix>_errors.pb.go` | `ErrorXxx` and `IsXxx` helpers | At least one enum value has a valid OpenKratos error annotation |
| `<prefix>_http.pb.go` | OpenKratos HTTP client and server bindings | The file has an applicable HTTP binding under the configured HTTP rules |
| `<prefix>_openkratos.pb.go` | Service middleware plans and enabled HTTP or gRPC wrappers | The file contains a service requiring generated OpenKratos wiring |
| `<prefix>_operation.pb.go` | Shared compile-time operation facts | The approved operation contract requires a separate transport-neutral artifact |

The operation output is reserved for the focused operation-contract workstream.
It must not be emitted as an empty placeholder before that contract is frozen.
Generated files may refer to types produced by `protoc-gen-go` and
`protoc-gen-go-grpc`; they must not reproduce those upstream definitions.

## Selection and Options

Error and HTTP output is descriptor-driven, and each pass skips files to which
it does not apply. Middleware plans are derived from service methods, not from
middleware names stored in Protobuf. Transport-wrapper options state which
upstream transport outputs an application generates; a wrapper for a disabled
or absent transport is not emitted.

Standard `protogen` path options remain available. Options owned by one pass
must use a pass-specific prefix such as `http_`; an HTTP option must not change
error or middleware output. The cutover implementation must document exact
replacements for the inherited `omitempty` and `omitempty_prefix` options
without changing their route semantics implicitly.

There is no general `features=errors,http,middleware` switch. A declared error
annotation or an enabled transport must not silently lose required generated
code because a second feature list drifted from the source and transport
configuration.

## Analysis and Diagnostics

The plugin follows an analyze-then-emit pipeline:

1. Read all files selected for generation and resolve public OpenKratos and
   upstream extension descriptors.
2. Build immutable per-file and per-service facts shared by relevant passes.
3. Validate identifiers, annotations, HTTP bindings, output collisions, and
   required upstream generated contracts.
4. Emit separate deterministic files only after analysis succeeds.

Invalid input returns a generation error. Generator code must not panic for a
user-authored invalid annotation, silently skip a malformed declaration, or
return a successful partial response. Diagnostics identify the proto file and,
where applicable, the service, method, enum, field, option, and offending
value.

The plugin advertises only Protobuf features and Editions that every applicable
pass handles correctly. Consolidation therefore requires the error,
middleware, and adapter passes to meet the same Open/Opaque and Editions
validation bar as the HTTP pass before the old executables are removed.

## Runtime and Release Contract

All OpenKratos-generated files carry the same generator version in their
header. Each output also uses the narrowest compile-time runtime compatibility
assertion for the package it consumes. A single generator version does not
replace transport-specific compatibility checks.

The unified generator is one Go module and one independently installable
release artifact. Its release records:

- the generator version and source commit;
- the minimum compatible OpenKratos runtime version;
- the compatible `github.com/openkratos/api` version and descriptor identity;
- the supported Go, Protobuf, and Editions range;
- clean external-consumer generation and compilation evidence.

The published module must not contain a repository-relative `replace`. A
release is incomplete until this succeeds outside the OpenKratos checkout:

```shell
GOWORK=off go install github.com/openkratos/kratos/cmd/protoc-gen-go-openkratos@<version>
```

Consolidation reduces installation, versioning, descriptor analysis, and
diagnostic complexity. It makes no request-path performance claim by itself;
runtime improvements must be demonstrated by generated code and focused
benchmarks for the affected transport.

## Migration

The generator cutover is intentionally breaking. OpenKratos does not retain
wrapper binaries at the old command paths.

An application migration performs these steps in one generated-code change:

1. Remove `protoc-gen-go-http` and `protoc-gen-go-errors` from tool dependencies
   and Buf or `protoc` configuration.
2. Add and pin `protoc-gen-go-openkratos`.
3. Translate documented generator options to their namespaced replacements.
4. Delete files produced by the old generators and regenerate from `.proto`
   source.
5. Review the generated diff by output responsibility rather than editing
   generated files.
6. Run compilation and transport/error behavior tests against the paired
   runtime version.

The migration guide must include a concrete Buf before/after example and an
option mapping before the old modules are removed. Expected filename and header
changes are generated churn; route availability, error codes, middleware order,
and RPC behavior are semantic changes and require separate documentation and
tests.

Current-behavior documentation continues to name the two old generators until
the unified executable is implemented and validated. The implementation change
updates current behavior and both language versions of the Kratos migration
guide in the same commit.

## Implementation Sequence

1. Move reusable analysis and rendering code behind internal pass boundaries
   without changing generated behavior.
2. Add the unified entry point and prove equivalent error and HTTP output for
   existing fixtures, except for generator headers and documented filenames.
3. Modernize error diagnostics and Editions support required by the common
   feature declaration.
4. Add middleware plans and transport wrappers from their approved contract.
5. Add external-consumer fixtures using `protoc-gen-go`,
   `protoc-gen-go-grpc`, and `protoc-gen-go-openkratos` together.
6. Update migration and current-behavior documentation, then remove the old
   generator modules and commands.
7. Publish and verify the unified generator before releasing generated code
   that requires it.

## Acceptance Gates

The consolidation is complete only when:

- one invocation generates every applicable OpenKratos artifact;
- files without relevant declarations produce no empty OpenKratos output;
- error-only, HTTP-only, gRPC-only, and combined fixtures compile;
- middleware plans affect HTTP and OpenKratos gRPC wrappers consistently;
- invalid input produces deterministic, source-qualified errors and no partial
  generated response;
- Open and Opaque Protobuf API fixtures pass for every output pass;
- output is deterministic across repeated generation;
- focused generator tests, root runtime tests, `go vet`, and relevant race tests
  pass;
- a clean external consumer installs released plugins and builds without
  `go.work`, local paths, or `replace` directives;
- migration documentation contains exact command, option, file, and generated
  API rewrites.
