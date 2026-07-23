# OpenKratos Public Protobuf API Module

Status: proposed implementation contract

Last reviewed: July 23, 2026

## Purpose

This document defines the source, package, release, and dependency boundary for
public OpenKratos Protobuf contracts. It also defines how the inherited Kratos
error schemas are replaced by one OpenKratos-owned contract.

OpenKratos is treated as a new framework. Kratos source compatibility is not a
constraint on the core API. Users that adopt OpenKratos from Kratos receive an
explicit migration path; legacy names and duplicate schemas do not remain in
the new framework solely to make that migration invisible.

Generated middleware wiring is defined separately in
[`generated-middleware.md`](generated-middleware.md). It intentionally has no
public Protobuf schema. Runtime modernization and the typed operation path are
defined in
[`runtime-modernization.md`](runtime-modernization.md). Generator ownership and
consolidation are defined in
[`protobuf-generation.md`](protobuf-generation.md).

## Decisions

The following decisions are fixed for implementation planning:

1. Public schemas live in a dedicated Git repository named `openkratos/api`.
2. That repository publishes the Go module `github.com/openkratos/api`.
3. The same source is published as the BSR module
   `buf.build/openkratos/api`.
4. Public Protobuf packages and file paths are versioned from their first
   OpenKratos release.
5. Every public contract has exactly one hand-maintained `.proto` source.
6. The OpenKratos runtime and unified generator consume the same generated Go API
   package. They do not generate private copies of public descriptors.
7. The three inherited `errors.proto` sources are removed after consumers move
   to the OpenKratos API module.
8. Compatibility code, source rewriting, and transition advice live in a
   migration tool or migration documentation, not in the core runtime.
9. Third-party schemas remain dependencies owned by their publishers. They are
   not copied into or republished from the OpenKratos API module.
10. Middleware wiring and deployment policy are generated or configured in Go;
    neither is published as an OpenKratos Protobuf contract.
11. OpenKratos-owned Go generation is released through one
    `protoc-gen-go-openkratos` plugin; upstream Go and gRPC generators remain
    independently owned plugins.

These decisions deliberately replace the inherited module topology. They do
not promise that `errors/errors.proto`, `package errors`, extension numbers
`1108` and `1109`, or `github.com/go-kratos/...` Go imports remain valid.

## Product Boundary

The public API repository owns portable contracts shared by more than one
consumer:

- error status and error enum annotations;
- generated Go bindings for those contracts;
- Buf lint, build, breaking-change, and publication configuration;
- release metadata connecting a Git tag, Go module version, BSR commit, and
  descriptor digest.

It does not own:

- OpenKratos runtime middleware implementations;
- HTTP or gRPC transport implementations;
- application configuration, credentials, secrets, or provider SDKs;
- generated service bindings for a business repository;
- vendored Google, Envoy, Buf, validation, or OpenAPI schemas;
- migration-only aliases for old Kratos packages.

This boundary keeps the schema dependency small. A business repository can use
OpenKratos annotations without importing the complete framework runtime.

## Artifact Topology

| Artifact | Identity | Responsibility |
| --- | --- | --- |
| Git repository | `github.com/openkratos/api` | Canonical schema source, generated Go bindings, release automation |
| Go module | `github.com/openkratos/api` | Versioned generated Go packages consumed by runtime, generators, and business code |
| BSR module | `buf.build/openkratos/api` | Versioned Protobuf source and descriptor distribution |
| Runtime module | `github.com/openkratos/kratos` | Error behavior, transports, middleware compilation, application runtime |
| OpenKratos generator | `github.com/openkratos/kratos/cmd/protoc-gen-go-openkratos` | Reads OpenKratos and upstream descriptors and emits independently scoped OpenKratos Go artifacts |

The Git repository, Go module, and BSR module are separate release artifacts
even though the first two share a repository. Release evidence must identify
all three explicitly.

## Target API Repository

The intended layout is:

```text
api/
|-- go.mod                         # module github.com/openkratos/api
|-- buf.yaml                       # buf.build/openkratos/api
|-- buf.lock
|-- buf.gen.yaml
|-- proto/
|   `-- openkratos/
|       `-- errors/
|           `-- v1/
|               `-- errors.proto
|-- errors/
|   `-- v1/
|       `-- errors.pb.go
|-- internal/
|   `-- releasecheck/
`-- README.md
```

The `proto/` directory is the BSR source root. Consumers import:

```proto
import "openkratos/errors/v1/errors.proto";
```

Generated Go code imports:

```go
import "github.com/openkratos/api/errors/v1"
```

Source files and generated files are kept in different directories. Generated
files are committed so ordinary Go consumers do not need `buf`, `protoc`, or a
networked code-generation step.

## Module Configuration

The API repository should use one Buf v2 module rooted at `proto/`. The exact
syntax must be verified against the pinned Buf CLI, but the semantic target is:

```yaml
version: v2
modules:
  - path: proto
    name: buf.build/openkratos/api
lint:
  use:
    - STANDARD
breaking:
  use:
    - FILE
```

Generation must be deterministic and use repository-pinned plugins. Managed
mode may set language options only when the resulting package path is part of
the reviewed public contract. A release job must fail if regeneration changes
committed output.

The Go module starts at the minimum Go version required by generated
`google.golang.org/protobuf` code. It must not inherit the runtime module's
larger dependency graph.

## Error Contract

### Namespace

The first OpenKratos error contract uses:

| Property | Value |
| --- | --- |
| File | `openkratos/errors/v1/errors.proto` |
| Protobuf package | `openkratos.errors.v1` |
| Go package | `github.com/openkratos/api/errors/v1;errors` |
| BSR module | `buf.build/openkratos/api` |

The version belongs in the Protobuf package and file path. A future incompatible
schema is introduced as `v2`; it does not silently redefine `v1`.

### Initial semantic scope

The first OpenKratos schema keeps the mature parts of the existing error model
without inheriting its package identity:

```proto
message Status {
  int32 code = 1;
  string reason = 2;
  string message = 3;
  map<string, string> metadata = 4;
}
```

This is the canonical OpenKratos error envelope. It is not an alias for, a
subset of, or a migration toward `google.rpc.Status`. In particular,
`google.rpc.Status` has no direct fields for the stable OpenKratos `reason` or
the string `metadata` map and therefore cannot preserve this contract through a
transparent substitution. Protocol adapters may map an OpenKratos error to a
transport-native status, but the OpenKratos HTTP/JSON and runtime error model
continue to expose all four fields above.

It also defines enum-level default status annotation and enum-value status
override annotation under the `openkratos.errors.v1` namespace. Their first
release preserves the current status-code meaning so module extraction is not
combined with a separate error-semantics redesign.

The source shape is:

```proto
syntax = "proto3";

package openkratos.errors.v1;

option go_package = "github.com/openkratos/api/errors/v1;errors";

import "google/protobuf/descriptor.proto";

message Status {
  int32 code = 1;
  string reason = 2;
  string message = 3;
  map<string, string> metadata = 4;
}

extend google.protobuf.EnumOptions {
  int32 default_code = 500101;
}

extend google.protobuf.EnumValueOptions {
  int32 code = 500102;
}
```

For v1, `code` retains the framework's status-code contract and transport
mapping, `reason` is the stable machine-readable identity, `message` is the
human-readable explanation, and `metadata` carries bounded string attributes.
Changing those semantics is a separate versioned error-contract decision.

### Local prototype evidence

The contract currently exists in the sibling local repository
`../OpenKratos-api` at commit `86742bf`. It has not been published to GitHub or
the Buf Schema Registry. The prototype uses these intended public identities so
that local generation exercises the future import boundary:

- Go module: `github.com/openkratos/api`;
- Buf module: `buf.build/openkratos/api`.

The local `make all` gate passes and covers Buf lint/build/generate, Go tests
and vet, descriptor-set construction, wire and ProtoJSON round trips, custom
option registration, optional-field presence, and an independent nested
consumer module. That consumer temporarily uses a local `replace`; publication
acceptance still requires repeating the consumer test against released Go and
BSR artifacts with no `replace` or `go.work` dependency.

The OpenKratos v1 allocation is:

| Extension | Extendee | Field number |
| --- | --- | --- |
| `openkratos.errors.v1.default_code` | `google.protobuf.EnumOptions` | `500101` |
| `openkratos.errors.v1.code` | `google.protobuf.EnumValueOptions` | `500102` |

These numbers are part of the proposed v1 contract. Before the schema is first
published, implementation must:

1. inventory custom `EnumOptions` and `EnumValueOptions` extensions in all
   direct schema dependencies;
2. verify `500101` and `500102` are outside reserved ranges and do not conflict;
3. record the allocation and collision scan in the API repository;
4. compile a descriptor set containing all dependencies to prove the numbers do
   not conflict;
5. stop for an explicit contract revision if either number is unavailable;
6. freeze the selected numbers for the lifetime of `v1`.

The inherited numbers `1108` and `1109` are migration inputs, not required
OpenKratos identities.

### Runtime ownership

The runtime package `github.com/openkratos/kratos/errors` owns Go error behavior:

- implementing `error`, `Unwrap`, `Is`, and conversion helpers;
- mapping the portable status to HTTP and gRPC responses;
- cloning metadata without sharing mutable maps;
- preserving causes without serializing private Go implementation details.

It does not own a second generated Protobuf message. Its error value wraps or
embeds `errorapi.Status` from `github.com/openkratos/api/errors/v1`, using an
internal alias to distinguish the API contract from the runtime `errors`
package.

The public API module must remain usable without the runtime module. The
dependency direction is always:

```text
github.com/openkratos/api
        ^
        |
        +-- github.com/openkratos/kratos
        `-- github.com/openkratos/kratos/cmd/protoc-gen-go-openkratos
```

The API module must never import the runtime or generator modules.

### Generator ownership

The error pass in `protoc-gen-go-openkratos` imports
`github.com/openkratos/api/errors/v1` to read the registered extension
descriptors. It must not contain:

- a private hand-maintained `errors.proto`;
- a private generated binding for the same public file;
- a dependency on `github.com/openkratos/kratos/errors` merely to read schema
  annotations.

Generated business helpers may depend on the runtime error package for behavior
and the API package for descriptors. The generator must report unsupported or
missing annotations as explicit generation errors rather than silently
producing incomplete helpers.

## Removal of Inherited Sources

The OpenKratos repository currently contains three inherited sources:

```text
errors/errors.proto
cmd/protoc-gen-go-errors/errors/errors.proto
third_party/errors/errors.proto
```

The target state contains none of them. After the new API module is published
and the runtime and generator compile against it:

- remove all three `.proto` files;
- remove their duplicate generated bindings;
- remove the inherited named Buf modules
  `buf.build/kratos/apis` and
  `buf.build/go-kratos/protoc-gen-go-errors` from active configuration;
- keep historical references only in migration documentation and repository
  history;
- add CI checks that reject a second hand-maintained definition of the public
  error file or extensions.

No OpenKratos release job publishes new content under a Kratos-owned BSR name.

## Release Contract

### Version relation

Every API release records:

- Git commit and signed tag;
- Go module semantic version;
- BSR immutable module commit and digest;
- Buf and Protobuf generator versions;
- generated-source clean-tree proof;
- minimum compatible OpenKratos runtime and generator versions.

The Go module and BSR module are generated from the same Git commit. Their
descriptors must be equivalent. A convenient BSR label may mirror the Go tag,
but consumers and release evidence use immutable identities.

### Release order

The required order is:

1. Merge and tag the API repository source.
2. Generate, test, and publish `github.com/openkratos/api`.
3. Publish `buf.build/openkratos/api` from the same commit.
4. export the published BSR descriptor and compare it with the release build;
5. release OpenKratos runtime modules that depend on that API version;
6. release the unified generator module against the published runtime/API
   versions;
7. verify a clean external consumer without `go.work` or `replace` directives.

A runtime or generator release must not reference an unpublished pseudo-version
or a repository-relative replacement.

## Migration Boundary

Kratos migration is supported, but it is not implemented as a permanent core
compatibility layer.

Migration support may provide:

- a source rewrite from `errors/errors.proto` to
  `openkratos/errors/v1/errors.proto`;
- package-qualified option rewrites;
- `buf.yaml` and `buf.lock` dependency changes;
- Go import rewrites where generated code or handwritten code names old
  packages;
- regeneration commands and generated-diff classification;
- adapters for data conversion when old serialized status messages must be
  read during a bounded data migration;
- a report mode that identifies unsupported custom patterns without changing
  files.

Migration support must not add the following to the core runtime:

- duplicate descriptor registrations;
- old BSR module dependencies;
- process-global alias registries;
- request-time schema translation;
- permanent old-package forwarding layers.

The migration tool is versioned independently. It may understand both old and
new contracts because its purpose is transition, not runtime execution.

## Implementation Phases

### Phase 0: Repository and ownership

- Create or confirm the `openkratos/api` Git repository.
- Confirm the BSR organization and `buf.build/openkratos/api` name.
- Establish maintainers, branch protection, release credentials, and
  least-privilege publication jobs.
- Pin Buf, protoc plugins, Go, and `google.golang.org/protobuf` versions.

Stop if repository ownership, BSR ownership, or release credentials are not
available. Do not publish under a temporary owner.

### Phase 1: Error API v1

- Add the versioned error source and generated Go package.
- Perform and record extension-number collision analysis.
- Add descriptor, generated-code, JSON, wire, and option-reading tests.
- Publish a release candidate and exercise it from an external consumer.

Stop if source and generated descriptors differ or if a clean consumer needs
local repository state.

### Phase 2: Runtime adoption

- Change runtime error values to use `errorapi.Status`.
- Preserve explicit Go error-chain and transport conversion behavior selected
  for OpenKratos.
- Remove the root generated error schema and update focused tests.
- Record benchmark changes separately from semantic changes.

### Phase 3: Generator adoption

- Change the unified generator's error pass to read extensions from the API
  module.
- Remove its local schema and generated binding.
- Verify Open and Opaque Protobuf APIs across every implemented pass.
- Verify versioned `go install` and external generation with `GOWORK=off`.

### Phase 4: Repository cleanup

- Remove the `third_party` error schema and inherited Buf module names.
- Add duplicate-source and generated-drift CI checks.
- Update examples to use the versioned OpenKratos import.
- Confirm no active source, build file, or lockfile depends on legacy modules.

### Phase 5: Migration support

- Add a migration specification with exact rewrite rules.
- Test migration on at least one realistic Kratos service repository.
- Require idempotent dry-run and apply modes.
- Keep migration fixtures outside the runtime dependency graph.

Middleware wiring has no schema publication step. The unpublished `policy/v1`
prototype is removed before the first API release and is not replaced by a
`middleware/v1` package.

## Validation Contract

The implementation must provide reproducible evidence for:

### API repository

```bash
buf lint
buf build
buf generate
go test ./...
go vet ./...
git diff --exit-code
```

### OpenKratos runtime

```bash
go test ./errors ./transport/http ./transport/grpc
go test -race ./errors ./transport/http ./transport/grpc
go vet ./errors ./transport/http ./transport/grpc
```

### OpenKratos generator

```bash
cd cmd/protoc-gen-go-openkratos
GOWORK=off go test ./...
GOWORK=off go vet ./...
```

### External consumer

The acceptance consumer must:

- use released API, runtime, and generator versions;
- use the published BSR module by immutable commit or lockfile;
- contain no `replace`, vendored OpenKratos schema, or repository-relative path;
- import the versioned error schema;
- use enum-level and enum-value annotations;
- generate, compile, and execute error helper assertions;
- pass with both supported Protobuf API modes.

## Security and Supply Chain

- BSR and Go publication credentials are restricted to protected release jobs.
- Pull requests run build, lint, breaking, generation, and test checks without
  publication credentials.
- Generated files must not contain local absolute paths or credential-bearing
  configuration.
- The release records dependency versions and generated artifact provenance.
- Public custom options are treated as untrusted input by generators; invalid
  values produce bounded, actionable errors.
- The API repository does not run user-provided plugins during release.

## Definition of Done

This work is complete only when:

- `github.com/openkratos/api` is independently installable;
- `buf.build/openkratos/api` is independently consumable;
- both artifacts are reproducible from the same reviewed Git commit;
- error contracts use the versioned OpenKratos namespace;
- exactly one hand-maintained OpenKratos error schema exists;
- the runtime and unified generator consume the same generated API package;
- the three inherited local schemas and two inherited Buf module names are gone
  from active configuration;
- external generation and runtime tests pass without local replacements;
- CI detects descriptor, generated-code, and publication drift;
- migration instructions exist without adding legacy dependencies to the core.
