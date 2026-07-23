# Atomic Protobuf Generation

Status: local atomic cutover implemented; public Buf publication pending

Last reviewed: July 23, 2026

## Purpose

This document defines the target ownership, packaging, diagnostics, release,
and compatibility contract for OpenKratos-owned Go Protobuf generators.

The current checkout ships the three atomic commands and no transitional
`protoc-gen-go-openkratos` executable. The intended Buf plugins are not yet
published; local command and fixture evidence must not be presented as public
BSR acceptance.

The public annotation schemas are defined by
[`public-protobuf-api-module.md`](public-protobuf-api-module.md). Generated
middleware plans are defined by
[`generated-middleware.md`](generated-middleware.md). Google HTTP transcoding
behavior is defined by
[`google-http-transcoding.md`](google-http-transcoding.md).

## Decision

OpenKratos publishes three independently selectable Buf plugins:

| Buf plugin | Executable | Protoc flag | Responsibility |
| --- | --- | --- | --- |
| `buf.build/openkratos/go-errors` | `protoc-gen-go-errors` | `--go-errors_out` | Business error helpers |
| `buf.build/openkratos/go-http` | `protoc-gen-go-http` | `--go-http_out` | HTTP bindings and Google HTTP transcoding |
| `buf.build/openkratos/go-middleware` | `protoc-gen-go-middleware` | `--go-middleware_out` | Service middleware plans and HTTP/gRPC wrappers |

The executable names deliberately omit `openkratos`. The Buf owner namespace
already supplies project identity, and repeating it in every plugin and binary
adds no disambiguating information.

The upstream generators remain independent:

- `protoc-gen-go` owns protobuf messages and descriptors;
- `protoc-gen-go-grpc` owns gRPC clients, servers, stream interfaces, handlers,
  and `grpc.ServiceDesc`.

OpenKratos does not wrap, fork, or republish those upstream generators.

## Why Atomic Plugins

Buf owns plugin distribution, execution, and version pinning for the supported
workflow. Installation convenience is therefore not a reason to couple
unrelated generation responsibilities into one executable.

Atomic plugins provide:

- one responsibility and option namespace per executable;
- independent release and rollback of errors, HTTP, and middleware generation;
- accurate generated-file headers and compatibility requirements;
- focused fixtures, diagnostics, and compatibility matrices;
- no requirement to run HTTP generation for an errors-only schema;
- no requirement to release HTTP code after an errors-only change;
- explicit selection of the generated surfaces an application consumes.

The split is packaging and ownership, not source duplication. Shared descriptor,
naming, diagnostic, and test infrastructure remains ordinary Go code reused by
the three commands.

## Source Topology

The target repository layout is:

```text
cmd/
  go.mod
  internal/generator/
    diagnostics/
    naming/
    testutil/
  protoc-gen-go-errors/
  protoc-gen-go-http/
  protoc-gen-go-middleware/
```

The three commands share one generator-focused Go module so generator-only
dependencies do not enter the runtime module. A shared package is added only
for behavior used by at least two plugins; pass-specific analysis and rendering
stay beside the command that owns them. No `common`, `utils`, pass registry, or
generic plugin framework is introduced.

The source module identity is fixed as:

```text
module github.com/openkratos/kratos/cmd
tag prefix: cmd
```

`modules.json` records that single Go module. The three Buf plugins remain
independent published artifacts; their BSR revisions are not Go module tags.

Each executable has its own `main` package, flags, and fixtures. Each published
Buf plugin has its own version and release artifact even though the command
sources share one Go module. A plugin must not invoke another plugin, parse
another plugin's generated Go source, or rely on execution order for descriptor
analysis.

## Output Ownership

For `service.proto`, applicable plugins emit separate deterministic files:

```text
service_errors.pb.go
service_http.pb.go
service_middleware.pb.go
```

Applicability is descriptor-driven:

- `go-errors` emits output only when the file declares supported error enum
  annotations;
- `go-http` emits output only when the selected HTTP policy produces bindings;
- `go-middleware` emits plans for every service in a generated file and only the
  transport wrappers explicitly enabled by its options.

No empty placeholder file is emitted. Generated headers name the exact plugin
and version that produced the file.

The plugins do not share ownership:

- `go-errors` does not emit services or transport code;
- `go-http` does not emit error helpers or middleware plans;
- `go-middleware` does not parse HTTP paths, generate codecs, copy gRPC
  registration, or emit transport clients.

## Options

Standard `protogen` path options remain available to every plugin.

`go-http` owns the inherited HTTP options without a cross-pass prefix:

```text
omitempty
omitempty_prefix
```

`omitempty` defaults to `true`; `omitempty_prefix` defaults to the empty string.
With `omitempty=true`, only RPCs with inline `(google.api.http)` annotations are
included. With `omitempty=false`, unannotated unary RPCs also receive generated
default POST bindings. Streaming RPCs never receive implicit default bindings.

`go-errors` has no feature selector. Supported annotations in the descriptor
determine whether it emits output.

`go-middleware` accepts transport-wrapper options:

```text
http=annotated
http=all
grpc=true
```

The `http` option is omitted by default and accepts exactly one method-set mode:

- `http=annotated` matches `go-http omitempty=true`;
- `http=all` matches `go-http omitempty=false`.

`grpc` defaults to `false`. These options assert that the corresponding
generated service interfaces are part of the application's generation pipeline.
They do not cause `go-middleware` to generate HTTP or gRPC wire bindings. An
omitted or disabled wrapper is not emitted. Unknown HTTP modes and invalid
boolean values are generation errors.

The duplicated HTTP policy is deliberate and compile-time checked: a
`go-middleware` HTTP mode that does not match the `go-http` `omitempty` setting
produces incompatible generated interfaces and fails compilation instead of
silently allowing a route to bypass its service plan. External Service Config
is not supported, so inline annotations and the optional default-binding policy
are the complete HTTP method-set inputs.

There is no general `features=errors,http,middleware` option. Buf plugin entries
already select responsibilities, and duplicating that selection inside a second
feature list creates drift.

## Buf Configuration

The intended published configuration is:

```yaml
plugins:
  - remote: buf.build/protocolbuffers/go:v1.36.11
    out: gen/go
    opt: paths=source_relative

  - remote: buf.build/grpc/go:v1.6.1
    out: gen/go
    opt: paths=source_relative

  - remote: buf.build/openkratos/go-errors:<version>
    out: gen/go
    opt: paths=source_relative

  - remote: buf.build/openkratos/go-http:<version>
    out: gen/go
    opt: paths=source_relative,omitempty=true

  - remote: buf.build/openkratos/go-middleware:<version>
    out: gen/go
    opt: paths=source_relative,http=annotated,grpc=true
```

Applications omit plugin entries for surfaces they do not use. Every remote
plugin revision is pinned directly in the repository's reviewed `buf.gen.yaml`;
examples must not use floating development revisions. `buf.lock` continues to
lock schema module dependencies and is not treated as the plugin-version lock.

## Analysis and Diagnostics

Each plugin independently follows an analyze-then-emit pipeline:

1. Read every file selected for that plugin invocation.
2. Resolve the public OpenKratos and upstream descriptors it owns.
3. Build immutable per-file and per-service facts.
4. Validate annotations, identifiers, output collisions, and required generated
   contracts.
5. Emit deterministic files only after that plugin's analysis succeeds.

Invalid user-authored input returns a generation error rather than panicking,
logging and continuing, or silently omitting output. Diagnostics identify the
proto file and, where applicable, service, method, enum, field, option, and
offending value.

Shared diagnostic helpers may standardize source qualification and formatting.
They must not turn the plugins into runtime-loaded passes or erase plugin-specific
error ownership.

## Cross-Plugin Contracts

Splitting executables does not remove generated API dependencies. It makes them
explicit:

- generated error helpers consume the published OpenKratos error descriptor and
  runtime errors API;
- generated HTTP bindings consume the documented `transport/http` support
  version;
- HTTP middleware wrappers compile against the interface emitted by `go-http`;
- gRPC middleware wrappers compile against interfaces emitted by
  `protoc-gen-go-grpc`;
- every middleware wrapper consumes the public OpenKratos middleware ABI.

Each generated file carries the narrowest compile-time support assertion for
the runtime package it consumes. The compatibility matrix records plugin,
OpenKratos API, runtime, `protoc-gen-go`, and `protoc-gen-go-grpc` versions.

A missing companion plugin or incompatible generated interface must fail at
generation or Go compilation with an actionable diagnostic. No runtime
reflection fallback, generated-source parser, or request-time adapter hides a
version mismatch.

## Release Contract

The three Buf plugins are independent release artifacts and may be pinned or
rolled back independently. They may initially be published from the same Git
repository, source commit, and release train; that operational choice does not
merge their compatibility identities.

Every plugin release records:

- plugin identity, version, and source commit;
- supported Protobuf Editions and API levels;
- compatible OpenKratos API and runtime versions;
- compatible upstream Go and gRPC generator versions where applicable;
- deterministic generation and external-consumer evidence.

Buf publication replaces versioned `go install` as the distribution acceptance
gate. Local command builds remain development and CI checks, not the public
installation contract.

## Local Cutover

The cutover is intentionally breaking. OpenKratos does not retain a forwarding
`protoc-gen-go-openkratos` binary.

Implementation proceeds in this order:

1. Extract shared source helpers without changing current generated behavior.
2. Add the three command entry points and plugin-specific tests.
3. Prove output equivalence for existing fixtures except documented filenames,
   headers, and option names.
4. Add combined Buf fixtures for errors-only, HTTP-only, middleware-only,
   gRPC-only, and all-enabled generation.
5. Switch repository generation and examples to the local atomic commands.
6. Update current-behavior, compatibility, and migration documentation.
7. Remove the unified command, module path, options, and generated filenames.
8. Prove the repository and local external-consumer fixtures with `GOWORK=off`;
   local `replace` directives remain explicitly pre-publication only.

The implementation must not rewrite committed generated files until the new
commands produce complete deterministic replacements. A failed cutover is
reverted by restoring the previous Buf plugin entries and generated files in
one change, not by shipping both command families indefinitely.

## Publication Gate

Remote publication is a later acceptance gate and does not block the local
breaking cutover. Publication requires:

1. release candidates for all three Buf plugins;
2. released API and runtime Go modules with no repository-relative replacement;
3. a clean external consumer using pinned published BSR artifacts;
4. recorded plugin revisions, source commits, compatibility versions, and
   deterministic-generation evidence.

The public release is incomplete until those checks pass, even when the local
source cutover is complete.

## Validation Contract

Required evidence includes:

- plugin-specific unit and golden tests;
- Open and Opaque Protobuf API fixtures for every plugin;
- deterministic repeated generation;
- source-qualified diagnostics with no successful partial response;
- compile tests for every supported plugin combination;
- compile-time mismatch tests for missing or incompatible companions;
- `GOWORK=off go test ./...` and `go vet ./...` in the generator module;
- clean Buf generation with no committed diff;
- a local external-consumer fixture that invokes the three commands explicitly;
- for publication, a second external consumer using pinned published plugins
  and no local executable, `go.work`, repository-relative path, or `replace`
  directive.

Runtime tests remain owned by the runtime packages affected by each generated
surface. Generator consolidation or splitting makes no request-path performance
claim by itself.

## Definition of Done

The local atomic generation cutover is complete only when:

- each executable owns exactly one documented responsibility;
- current fixtures generate and compile through the atomic plugins;
- errors, HTTP, and middleware output can be selected independently;
- shared source code does not create a runtime pass registry or output coupling;
- generated compatibility failures are explicit and actionable;
- no active configuration invokes `protoc-gen-go-openkratos` or
  `--go-openkratos_out`;
- current-behavior and migration documentation names the atomic plugins.

Public release acceptance additionally requires:

- all three intended `buf.build/openkratos/*` plugins are published and pinned;
- external generation and compilation pass using published Buf artifacts and
  released Go modules, with no local repository dependency.
