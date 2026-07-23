# Proto-Declared Operation Policy and Precompiled Middleware

Status: proposed implementation contract

Last reviewed: July 23, 2026

## Purpose

This document defines how OpenKratos service contracts declare operation policy
in Protobuf and how the framework turns those declarations into middleware
chains without request-time blacklists, whitelists, descriptor parsing, regular
expression matching, or repeated chain construction.

The public schema is distributed by the API module defined in
[`public-protobuf-api-module.md`](public-protobuf-api-module.md). Typed operation
entry points and broader runtime modernization are defined in
[`runtime-modernization.md`](runtime-modernization.md).

OpenKratos is a new framework. The core design is not constrained to preserve
Kratos selector behavior or middleware source compatibility. Migration support
may translate common Kratos selector configurations, but the new primary path
is descriptor-driven and fails early when required policy cannot be compiled.

## Problem

The inherited middleware model applies generic middleware and commonly relies
on operation-name selectors, prefix lists, regular expressions, or manually
maintained include/exclude lists. That creates four problems:

1. Service policy is split between `.proto`, bootstrap code, and deployment
   configuration.
2. Renaming an RPC can silently bypass a string-based rule.
3. Each request may repeat operation lookup, selector matching, slice
   allocation, and middleware-chain work that was knowable earlier.
4. HTTP and gRPC can disagree because each transport derives or matches policy
   independently.

The target design makes portable operation requirements part of the service
contract, generates one operation description, resolves implementations during
registration, and executes a precomposed chain on the request path.

## Decisions

1. Portable operation requirements are declared with Protobuf custom options.
2. Proto options describe required behavior, never Go constructors, import
   paths, provider SDKs, credentials, or environment-specific values.
3. Service defaults and method overrides use explicit field presence and a
   deterministic merge algorithm.
4. Every operation must resolve to an explicit access mode. Missing access
   policy is a generation or registration error, never implicit public access.
5. HTTP, gRPC, documentation, telemetry, and optional adapters consume the same
   generated operation metadata.
6. Runtime middleware implementations are injected into an application-owned
   compiler. There is no process-global provider registry.
7. Middleware chains are compiled once at service registration and attached to
   concrete operation handlers.
8. The successful request path performs no descriptor parsing, selector scan,
   policy lookup, or middleware-slice construction. HTTP handlers hold their
   operation directly; gRPC performs at most one immutable O(1) dispatch lookup
   by `FullMethod` because the transport API supplies that identifier at
   runtime.
9. Application middleware outside the policy system remains possible through
   explicit registration hooks. It does not change the meaning of policy
   annotations.
10. Migration from Kratos selectors is tooling work, not a permanent runtime
    compatibility branch.

## Contract Boundary

Proto may declare facts such as:

- public, authenticated, or authorized access;
- required permissions or resource actions;
- request validation requirement;
- audit requirement;
- intrinsic method idempotency and a named request-deduplication class;
- named rate class;
- named request-budget class;
- transport-independent operation identity and streaming shape.

Proto must not declare:

- a Go middleware name or constructor;
- a JWT library, issuer secret, key, or provider configuration;
- a Redis, database, queue, or vendor implementation;
- concrete production QPS, burst size, timeout duration, or retry count;
- tracing sample rates;
- deployment-specific bypass identities;
- arbitrary executable expressions.

Names such as `rate_class = "write"` and `budget_class = "interactive"` are
portable policy classes. Deployment configuration maps those classes to
concrete values and implementations.

## Public Proto Contract

### Location and package

| Property | Value |
| --- | --- |
| File | `openkratos/policy/v1/policy.proto` |
| Protobuf package | `openkratos.policy.v1` |
| Go package | `github.com/openkratos/api/policy/v1;policy` |
| BSR module | `buf.build/openkratos/api` |

### Proposed v1 shape

The implementation should preserve the following semantic shape. Custom option
field numbers are part of the proposed contract and are collision-checked in
the API repository before the first release.

```proto
syntax = "proto3";

package openkratos.policy.v1;

option go_package = "github.com/openkratos/api/policy/v1;policy";

import "google/protobuf/descriptor.proto";

enum Access {
  ACCESS_UNSPECIFIED = 0;
  ACCESS_PUBLIC = 1;
  ACCESS_AUTHENTICATED = 2;
  ACCESS_AUTHORIZED = 3;
}

message PermissionPolicy {
  repeated string require_all = 1;
  repeated string require_any = 2;
}

message OperationPolicy {
  optional Access access = 1;
  PermissionPolicy permissions = 2;
  optional bool validate_request = 3;
  optional bool audit = 4;
  optional string idempotency_class = 5;
  optional string rate_class = 6;
  optional string budget_class = 7;
}

extend google.protobuf.ServiceOptions {
  OperationPolicy default_policy = 500201;
}

extend google.protobuf.MethodOptions {
  OperationPolicy policy = 500202;
}
```

The API repository records that `500201` extends `ServiceOptions` and `500202`
extends `MethodOptions`. It must compile these extensions with the complete
direct dependency descriptor set. A collision stops publication and requires
an explicit contract revision; implementation must not choose a different
number silently.

### Why presence is required

Service policy and method policy must distinguish these states:

- absent: inherit the previous layer;
- present with a value: replace the previous layer;
- present `false`: explicitly disable an inherited boolean requirement;
- present empty permission message: explicitly clear inherited permissions;
- present empty idempotency, rate, or budget class: explicitly clear the
  inherited class.

Proto3 `optional` fields provide scalar presence. Message fields already have
presence. Repeated permissions are wrapped in `PermissionPolicy` so a method can
distinguish inheritance from an intentional empty set.

## Policy Resolution

Policy is resolved once per method in this order:

1. Start with an empty framework policy: access is unspecified, boolean
   capabilities are disabled, permissions are empty, and classes are disabled.
2. Apply every present field from the service `default_policy`.
3. Apply every present field from the method `policy`.
4. Normalize strings and permission sets.
5. Validate cross-field invariants.
6. Emit an immutable generated operation policy.

Method fields replace, rather than append to, the corresponding service field.
For `PermissionPolicy`, presence replaces the whole permission policy. Within a
single policy, duplicate permission strings are rejected rather than silently
deduplicated so contract mistakes remain visible.

After merging:

- `ACCESS_UNSPECIFIED` is invalid;
- `ACCESS_PUBLIC` cannot require permissions;
- `ACCESS_AUTHENTICATED` cannot require authorization permissions;
- `ACCESS_AUTHORIZED` must contain at least one `require_all` or `require_any`
  permission;
- a permission cannot appear in both lists;
- empty or whitespace-only permissions are invalid;
- a present empty idempotency, rate, or budget class clears that class, while a
  non-empty class must contain no leading or trailing whitespace;
- permission names match `^[a-z][a-z0-9._:-]{0,127}$`;
- non-empty idempotency, rate, and budget class names match
  `^[a-z][a-z0-9._-]{0,63}$`;
- an `idempotency_class` requires the standard protobuf
  `MethodOptions.idempotency_level` to be `IDEMPOTENT`;
- every named class must have a runtime binding before registration succeeds.

Intrinsic method semantics and request deduplication are intentionally
separate. The standard `MethodOptions.idempotency_level` states whether calling
the method repeatedly has the same intended effect. `idempotency_class` selects
an application binding that enforces request-key storage, conflict handling,
and replay behavior. OpenKratos does not duplicate the standard semantic fact
with a second boolean option.

Authorization succeeds only when every `require_all` permission is granted and,
when `require_any` is non-empty, at least one `require_any` permission is
granted. An empty list contributes no condition. Permission ordering has no
semantic meaning, but generators preserve declaration order for deterministic
diagnostics and generated output.

These checks run in generators when descriptors are available and run again at
registration as defense against dynamically assembled services.

## Example

```proto
service DocumentService {
  option (openkratos.policy.v1.default_policy) = {
    access: ACCESS_AUTHENTICATED
    validate_request: true
    audit: true
    budget_class: "interactive"
  };

  rpc GetDocument(GetDocumentRequest) returns (GetDocumentReply) {
    option idempotency_level = IDEMPOTENT;
    option (openkratos.policy.v1.policy) = {
      access: ACCESS_AUTHORIZED
      permissions: { require_all: "documents.read" }
      idempotency_class: "request-key"
      rate_class: "read"
    };
  }

  rpc Health(HealthRequest) returns (HealthReply) {
    option (openkratos.policy.v1.policy) = {
      access: ACCESS_PUBLIC
      validate_request: false
      audit: false
    };
  }
}
```

No Go bootstrap list repeats `DocumentService/GetDocument` or
`DocumentService/Health`. The generated operation metadata carries the resolved
policy for every transport.

## Generated Operation Model

Generators emit one transport-neutral immutable description per method. The
semantic model includes at least:

```go
type Operation struct {
	Name       string
	Service    string
	Method     string
	Streaming  Streaming
	Policy     Policy
}

type Policy struct {
	Access          Access
	ValidateRequest bool
	Audit           bool
	IdempotencyLevel descriptorpb.MethodOptions_IdempotencyLevel
	IdempotencyClass string
	RateClass       string
	BudgetClass     string
}

func (p Policy) RequireAll() iter.Seq[string]
func (p Policy) RequireAny() iter.Seq[string]
```

The actual generated Go API is frozen in the typed-operation implementation
proposal. The semantic requirements here are fixed:

- operation name is fully qualified and transport independent;
- policy is already merged and validated;
- generated policy collections are held in unexported storage and exposed only
  through read-only accessors or iterators; immutability is enforced by the API,
  not documented as a convention around exported slices;
- HTTP and gRPC bindings reference the same operation value;
- descriptor references are included only where a consumer genuinely needs
  reflection;
- no generated operation stores a middleware implementation or deployment
  secret.

Documentation and OpenAPI generators read the same resolved policy so public
access, permission requirements, intrinsic idempotency, and deduplication class
are not described from a second interpretation path.

## Runtime Policy Compiler

The runtime owns a concrete application-scoped compiler. It receives explicit,
capability-specific providers. Providers expose the semantics needed by their
capability; they do not return arbitrary `Middleware` values that could change
stage order or smuggle unrelated behavior into the compiled policy chain. A
representative dependency shape is:

```go
type Providers struct {
	Authenticator Authenticator
	Authorizer     Authorizer
	Validator      Validator
	Auditor        Auditor
	Deduplicator   Deduplicator
	Limiter        Limiter
	Budgeter       Budgeter
}

func NewCompiler(providers Providers) (*Compiler, error)
func (c *Compiler) CompileUnary(op Operation) (UnaryHandler, error)
```

This is a design shape, not approval of these exact exported identifiers. The
implementation proposal must freeze each capability interface and align it with
the typed handler ABI. Authentication returns identity-bearing context,
authorization makes a permission decision, validation checks a request,
limiting admits or rejects work, auditing observes a bounded outcome,
deduplication controls request-key execution/replay, and budgeting derives a
deadline. The compiler, not a provider, owns handler wrapping and stable stage
order. The following behavior is required regardless of naming:

- construction validates configured providers and class maps without requiring
  providers for capabilities unused by the application;
- compile is deterministic and safe for concurrent service registration;
- a required capability without a binding returns an actionable registration
  error naming the operation and capability;
- an invalid class name or unknown configured class fails registration;
- unused optional bindings do not add middleware to an operation;
- compiled chains contain no mutable application-global state owned by the
  framework;
- compilation errors are returned, not logged and ignored;
- request handling never falls back to string selectors after compile failure.

Providers may hold application-owned clients or configuration. The application
owns their shutdown. The policy compiler does not create hidden background
goroutines or process-global registries.

## Chain Order

OpenKratos defines stable policy stages. Outermost stages appear first:

```text
recovery
telemetry
request budget
authentication
authorization
operation rate limit
request validation
audit
idempotency
typed operation handler
```

The reasons for the order are:

- recovery and telemetry observe every downstream outcome;
- request budgets bound downstream policy and business work;
- authorization and identity-aware rate limits run only after authentication;
- validation occurs before business mutation;
- audit observes authenticated identity and the final downstream result;
- idempotency can return a stored operation result while remaining auditable.

Transport-level connection limits, body-size limits, CORS, decompression, and
edge denial-of-service controls are outside this operation-policy chain. They
may run earlier because they apply before an RPC operation is safely known.

Applications may add explicit outer or inner middleware through documented
registration hooks. They cannot reorder required policy stages or replace an
annotated requirement with a no-op without choosing a deliberately unsafe test
or development binding that is visible in configuration.

## Request Path

The target request flow is:

```text
transport route match
  -> generated operation pointer
  -> precompiled operation chain
  -> typed generated decoder/handler/encoder path
```

The steady-state path must not:

- call `proto.GetExtension`;
- walk method or service descriptors;
- construct operation names for selector lookup;
- match prefixes or regular expressions;
- allocate a middleware list;
- merge service and method policy;
- resolve policy class names;
- rebuild wrapper closures.

Descriptor interpretation and policy merging happen during generation where
possible. Provider resolution and chain construction happen during service
registration because they depend on application configuration.

Generated HTTP service registration returns `error`; it must not defer policy
compilation to the first request or log and continue after a missing binding.
gRPC registration builds one immutable dispatch table keyed by `FullMethod`.
Unary interception performs one constant-time lookup in that table and invokes
the precompiled handler. This lookup is transport dispatch, not policy
selection: it performs no descriptor read, selector match, merge, provider
resolution, or closure construction.

## Transport Semantics

The same operation policy applies to HTTP and gRPC. Transport adapters map
portable failures to their native status representation:

| Failure | Canonical meaning |
| --- | --- |
| Missing or invalid identity | unauthenticated |
| Identity lacks permission | permission denied |
| Request validation fails | invalid argument |
| Operation rate limit exceeded | resource exhausted |
| Request budget expires | deadline exceeded |
| Idempotency conflict | failed precondition or documented domain conflict |

The exact HTTP status and error body are owned by the error/transport contract,
not redefined by individual policy providers.

Provider implementations receive the request context and must return promptly
when it is canceled. They must not replace a shorter caller deadline with a
longer policy budget.

## Streaming Scope

Operation policy v1 is unary-only. If a client-streaming, server-streaming, or
bidirectional method declares `policy`, or inherits `default_policy`, generation
fails with the service, method, and streaming shape in the diagnostic. The
runtime does not silently apply unary authentication, audit, validation,
budget, rate, or deduplication semantics to a stream.

Streaming policy requires a separate contract for stream-open identity,
per-message authorization and validation, lifecycle audit, long-lived budgets,
rate accounting, and termination. It can be added in a later version without
making those choices implicitly in v1.

## Existing Application Middleware

OpenKratos may continue to expose explicit application middleware composition,
but it is separate from contract policy:

- global application middleware is registered explicitly and runs at a
  documented outer or inner hook;
- operation-specific contract middleware comes only from generated policy;
- a legacy selector adapter may exist in a migration module for a bounded
  transition, but core generated bindings never call it;
- one request never evaluates both a generated policy and a legacy selector for
  the same capability;
- migration tests compare context propagation, ordering, error propagation, and
  panic behavior before removing the adapter.

The migration target for a blacklist or whitelist is a Proto annotation, not a
different string-matching syntax.

## Failure and Security Semantics

- Absence of resolved access policy fails closed at generation or registration.
- Missing runtime capability bindings fail registration before the server is
  marked ready.
- Provider panics are handled by the normal recovery boundary and are not
  converted into authorization success.
- Policy metadata is bounded and validated; generators reject unbounded names
  and malformed permissions.
- Secrets and provider configuration never enter generated operation metadata,
  descriptors, documentation, or telemetry attributes.
- Telemetry records stable operation and policy class names, never arbitrary
  credentials, tokens, raw paths, or full permission sets by default.
- Authorization providers must not mutate generated operation metadata.
- Development bypass bindings must be explicit, locally configured, and
  impossible to activate through a request field or header alone.

## Performance Contract

Optimization is accepted only with isolated and end-to-end evidence. Required
benchmarks include:

- service registration with 1, 10, 100, and 1000 operations;
- request dispatch with no optional policy capability;
- authenticated and authorized paths;
- public-path dispatch;
- rate, validation, audit, and idempotency combinations;
- generated policy versus the inherited selector path;
- HTTP and gRPC transports;
- failure paths for authentication, authorization, rate limits, and budgets.

Measurements report time, allocations, bytes, and registration cost. Provider
business work is measured separately from framework dispatch so eliminating
selector overhead is not presented as eliminating necessary authentication or
authorization work.

The primary steady-state acceptance condition is structural: no repeated
policy discovery or chain construction. A throughput percentage is recorded as
evidence, not used as the only correctness gate.

## Migration Tooling

A Kratos-to-OpenKratos migration tool may:

1. inventory middleware registration and selector expressions;
2. resolve exact method sets using compiled descriptors;
3. propose service defaults and method overrides;
4. write or patch policy options through a Protobuf-aware parser;
5. report selectors that cannot be represented without a custom application
   hook;
6. update Buf dependencies and regenerate code;
7. compare the selected operation set before and after migration;
8. produce a reviewable report before applying changes.

It must not translate arbitrary regular expressions by textual guesswork. A
selector whose matched method set cannot be proven is a manual migration item.
Migration output is idempotent and supports dry-run mode.

## Implementation Phases

### Phase 0: Contract freeze

- Allocate and collision-check custom option field numbers.
- Compile the proposed schema under the pinned Protobuf toolchain.
- Freeze access, presence, merge, and streaming semantics.
- Add valid and invalid descriptor fixtures.

The local `../OpenKratos-api` prototype at commit `86742bf` satisfies the local
schema portion of this phase. It has not been published. Its `make all` gate
validates clean generation, descriptor and extension behavior, presence,
standard idempotency semantics plus `idempotency_class`, and a nested consumer
using a temporary local replacement.

The OpenKratos runtime repository now contains the shared descriptor resolver
in `internal/operationpolicy`. It implements deterministic service/method
merging, fail-closed access validation, permission and class validation,
standard idempotency checks, immutable permission results, and unary-only
enforcement. Generated operation values, runtime provider compilation, and
transport attachment remain later phases and are not current runtime behavior.

### Phase 1: API publication

- Add `policy/v1` to `github.com/openkratos/api`.
- Generate and test Go bindings.
- Publish matching Go and BSR artifacts.
- Verify Open and Opaque API option reads from a clean consumer.

### Phase 2: Generated operation metadata

- Define the typed transport-neutral operation model.
- Generate identical resolved metadata for HTTP and gRPC.
- Add descriptor-driven equivalence and fuzz tests.
- Make unsupported policy combinations generation errors.

### Phase 3: Runtime compiler

- Implement application-scoped bindings and compiler.
- Compile deterministic chains at service registration.
- Make missing bindings and unknown classes readiness-blocking errors.
- Add ordering, context, panic, and error-propagation tests.

### Phase 4: Transport adoption

- Attach generated operations and compiled chains to HTTP handlers.
- Make generated HTTP registration return compilation errors.
- Attach the same operations and policy semantics to a gRPC immutable unary
  dispatch table.
- Remove core request-time operation selectors for policy capabilities.
- Keep explicit generic application middleware hooks separate.

### Phase 5: Migration support

- Implement descriptor-aware selector inventory and annotation generation.
- Prove method-set equivalence on a representative Kratos service.
- Document unsupported selector patterns and manual escape hatches.
- Remove bounded adapters after their announced migration window.

## Validation Contract

Required validation includes:

- Buf lint, build, generation, and breaking checks for the API module;
- table-driven merge and validation tests for every presence combination;
- generator golden tests for service defaults and method overrides;
- Open and Opaque Protobuf API fixtures;
- HTTP/gRPC operation metadata equivalence tests;
- registration failure tests for missing providers and unknown classes;
- race tests for concurrent registration and request execution;
- context cancellation and deadline tests;
- unary and rejected-streaming operation tests;
- deterministic generated-source checks;
- external consumer tests without `go.work` or `replace`;
- before-and-after selector and dispatch benchmarks.

## Definition of Done

This work is complete only when:

- `openkratos/policy/v1/policy.proto` is published from the canonical API
  repository;
- access policy fails closed when it is not resolved;
- service defaults and method overrides have tested presence semantics;
- HTTP and gRPC reference identical generated operation policy;
- required runtime implementations are explicit application dependencies;
- every operation chain is compiled before readiness;
- the request path performs no policy descriptor parsing, selector scan,
  policy merge, provider lookup, or chain construction;
- unsupported streaming policy is rejected explicitly;
- generic application middleware remains available through documented hooks;
- migration tooling can translate provable selector sets without adding a
  legacy selector path to the core;
- correctness, race, external-consumer, and performance evidence is recorded.
