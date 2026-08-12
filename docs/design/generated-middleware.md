# Generated Middleware Wiring

Status: runtime and local atomic generator implemented; publication pending

Last reviewed: July 23, 2026

## Purpose

This document defines how Forge binds application middleware to generated
services without publishing middleware names or execution policy in Protobuf.
It covers server-side unary and streaming RPCs, HTTP and gRPC reuse,
transport-specific escape hatches, registration-time composition, and migration
from Forge selectors.

The public Protobuf module is defined in
[`public-protobuf-api-module.md`](public-protobuf-api-module.md). The wider
runtime plan is defined in
[`runtime-modernization.md`](runtime-modernization.md). The atomic generator
topology is defined in
[`protobuf-generation.md`](protobuf-generation.md).

## Decision

Forge does not publish a `middleware/v1` Protobuf package and does not
generate Go interface methods from arbitrary hook names in `.proto` files.
The unpublished `policy/v1` prototype is also removed before the first public
API release.

Middleware wiring is application code. Generated Go code exposes the real RPC
methods as fields in a service-specific middleware plan. Applications assign
ordinary middleware values to those fields and pass the plan to generated HTTP
and gRPC wrappers.

This preserves the useful properties of the earlier proposal:

- no request-time selector, string lookup, reflection, or descriptor read;
- deterministic service and method ordering;
- one registration-time composition step;
- one reusable configuration shape for HTTP and gRPC;
- explicit compile-time RPC field names.

It removes the earlier constraints:

- no service-sized interface that one application type must implement;
- no Go identifier stored in a language-neutral Protobuf contract;
- no invented semantic meaning for names such as `Authenticate`;
- no claim that transport-native middleware is portable;
- no attempt to model a stream as one unary request and reply.

## Boundaries

Generated middleware wiring is:

- server-side;
- application-owned;
- configured in Go;
- immutable after a generated wrapper is built;
- transport-neutral only where the runtime contract is genuinely shared.

It does not define:

- authentication, authorization, validation, audit, rate, budget, or
  idempotency semantics;
- credentials, provider SDKs, secrets, or deployment configuration;
- client middleware;
- raw `net/http` middleware or gRPC interceptors;
- a process-global registry;
- a middleware name, constructor, or import path in Protobuf.

Portable API semantics may receive separate Protobuf annotations when a real
cross-language contract exists. Such annotations describe the API requirement,
not the Go middleware that implements it.

## Runtime Contracts

Unary and stream middleware are separate public types:

```go
type UnaryHandler func(context.Context, any) (any, error)

type UnaryMiddleware func(UnaryHandler) UnaryHandler

type ServerStream interface {
	Context() context.Context
	SendMsg(any) error
	RecvMsg(any) error
}

type StreamHandler func(request any, stream ServerStream) error

type StreamMiddleware func(StreamHandler) StreamHandler
```

`UnaryMiddleware` retains the current transport-neutral request/reply model but
names its actual scope. `StreamMiddleware` wraps one complete stream lifecycle.
It does not run once per message by default. `request` is the decoded initial
request for a server-streaming method and is nil for client-streaming and
bidirectional methods, whose messages arrive through `RecvMsg`.

A stream middleware that needs per-message behavior decorates `ServerStream`
and intercepts `SendMsg` and `RecvMsg` before calling the next handler. EOF,
half-close, cancellation, and the handler's terminal error therefore remain
observable without building a second per-message middleware registry.

`ServerStream` is deliberately smaller than `grpc.ServerStream`. Common
middleware uses the context and message flow. HTTP response control, gRPC peer
information, native headers, compression, and other transport-only features use
the transport-specific layer described below.

A stream middleware that adds context values decorates `ServerStream` and
overrides `Context`; the common contract does not expose a mutable `SetContext`
method.

A stream middleware may pass a replacement initial request to the next handler,
but it must retain the generated request type. Generated terminal adapters
validate the type and return a service-and-method-qualified error instead of
panicking on an invalid replacement.

The names are `UnaryHandler` and `UnaryMiddleware`; there is no combined
`middleware.Middleware` or `middleware.Handler`. Forge is pre-v1 and does not
retain ambiguous aliases in the final core API solely for source
compatibility.

## Generated Service Plan

For a service with unary and streaming methods, the generator emits a zero-value
usable plan:

```go
type DocumentServiceMiddleware struct {
	Unary   []middleware.UnaryMiddleware
	Stream  []middleware.StreamMiddleware
	Methods DocumentServiceMethodMiddleware
}

type DocumentServiceMethodMiddleware struct {
	GetDocument []middleware.UnaryMiddleware
	Health      []middleware.UnaryMiddleware
	Watch       []middleware.StreamMiddleware
	Upload      []middleware.StreamMiddleware
	Chat        []middleware.StreamMiddleware
}
```

The generator derives fields only from the service descriptor's RPC methods.
Users do not declare middleware names in Protobuf and do not repeat operation
path strings in Go.

The plan semantics are fixed:

- `Unary` applies to every unary method in the service;
- `Stream` applies to client-streaming, server-streaming, and bidirectional
  methods in the service;
- a field under `Methods` appends middleware for that method;
- service middleware appears before method middleware;
- the first middleware in a slice is the outermost wrapper and runs first on
  entry;
- an empty plan applies no generated service middleware;
- duplicate middleware values are allowed because Go function values are not
  comparable and repeated application may be intentional;
- nil middleware values and middleware returning nil handlers fail wrapper
  construction before registration;
- wrapper construction snapshots every slice, so later mutation of the plan or
  its backing arrays has no effect.

Generated RPC field names use the same Go naming rules as generated service
methods. `Unary`, `Stream`, and `Methods` live in the outer plan while RPC names
live in `DocumentServiceMethodMiddleware`, so ordinary RPC names cannot collide
with the service-default fields.

## User Wiring

Middleware remains an ordinary Go function. It may be implemented in separate
packages and composed without an aggregate implementation type:

```go
plan := documentv1.DocumentServiceMiddleware{
	Unary: []middleware.UnaryMiddleware{
		authn.Authenticate(authenticator),
		observe.Requests(logger, meter),
	},
	Stream: []middleware.StreamMiddleware{
		authn.AuthenticateStream(authenticator),
		observe.Streams(logger, meter),
	},
	Methods: documentv1.DocumentServiceMethodMiddleware{
		GetDocument: []middleware.UnaryMiddleware{
			authz.RequireDocumentsRead(authorizer),
		},
		Watch: []middleware.StreamMiddleware{
			authz.RequireDocumentWatch(authorizer),
		},
	},
}
```

`Authenticate`, `RequireDocumentsRead`, and the other constructors above are
application APIs. Forge neither knows their names nor generates interfaces
for them.

The same plan may be used by both transports:

```go
httpService, err := documentv1.WrapDocumentServiceHTTPServer(service, plan)
if err != nil {
	return fmt.Errorf("building document HTTP service: %w", err)
}
documentv1.RegisterDocumentServiceHTTPServer(httpServer, httpService)

grpcService, err := documentv1.WrapDocumentServiceGRPCServer(service, plan)
if err != nil {
	return fmt.Errorf("building document gRPC service: %w", err)
}
documentv1.RegisterDocumentServiceServer(grpcServer, grpcService)
```

The wrapper names are generated alongside the transport bindings. The gRPC
wrapper preserves the standard `protoc-gen-go-grpc` registration function and
does not replace or mutate its `grpc.ServiceDesc`.

An application may pass different plan values to the HTTP and gRPC wrappers
when their application behavior intentionally differs. Reuse is supported, not
forced. A shared plan must contain only middleware whose semantics are valid for
both transports.

## Generator Ownership

The target `protoc-gen-go-middleware` plugin exclusively owns the service plan
and wrapper constructors. It emits one `_middleware.pb.go` file for each
business Proto file that declares services and only the transport wrappers
enabled by its options. Plans are generated even when both transport wrappers
are disabled. The current unified executable is transitional and is removed
after the atomic plugin cutover.

The HTTP wrapper option names the interface method set rather than a boolean:

- `http=annotated` wraps the interface emitted by `go-http omitempty=true`;
- `http=all` wraps the interface emitted by `go-http omitempty=false`;
- omitting `http` emits no HTTP wrapper.

`grpc=true` emits the gRPC wrapper and defaults to false. A mismatched HTTP
method-set configuration fails Go compilation; the generator never guesses or
discovers another plugin's options at runtime.

Wire behavior remains owned by the established generators:

- `protoc-gen-go` owns protobuf messages and descriptors;
- `protoc-gen-go-grpc` owns gRPC clients, server interfaces, stream interfaces,
  handlers, and `grpc.ServiceDesc`;
- `protoc-gen-go-http` owns HTTP routes, transcoding, clients, and HTTP stream
  adapters;
- `protoc-gen-go-middleware` owns plans and wrappers around generated service
  interfaces;
- `protoc-gen-go-errors` owns business error helpers.

The middleware pass must not copy HTTP path parsing, gRPC registration, codecs,
or transport dispatch from their owning passes or upstream generators. A wrapper
for a disabled or absent transport is not emitted. Generator output-name
collisions are diagnosed with the Proto file, service, and conflicting Go
identifier; identifiers are never silently renamed.

## Unary Execution

Each generated wrapper composes its unary handlers once during construction:

```go
handler := getDocumentHandler(service)
handler = middleware.ChainUnary(
	append(snapshot(plan.Unary), plan.Methods.GetDocument...)...,
)(handler)
```

The pseudocode describes ordering, not approval of an allocation-heavy
implementation. The generator emits direct immutable per-method data and the
runtime may preallocate the exact combined length.

HTTP performs route matching and request binding before invoking the composed
unary handler. gRPC-Go performs message decoding and its configured interceptors
before invoking the generated wrapper. Both pass the same protobuf request and
Forge server context to the shared unary chain.

## Stream Execution

One stream middleware invocation surrounds the entire generated service stream
handler:

```text
service stream middleware
  -> method stream middleware
    -> generated stream adapter
      -> business stream method
```

The generated HTTP and gRPC adapters present the common `ServerStream` contract
and preserve all four RPC shapes:

- server streaming;
- client streaming;
- bidirectional streaming;
- unary RPCs remain on the unary path and are never adapted as streams.

Message direction remains enforced by the generated business stream API. A
server-streaming business method cannot receive arbitrary client messages, and
a client-streaming method retains its generated close-and-reply behavior.

Lifecycle middleware observes the stream context, optional initial request,
handler entry, terminal error, and cancellation. Per-message middleware
decorates the supplied stream. Transport adapters must not invoke one middleware
slice around the stream handler and a second hidden slice around every `SendMsg`
or `RecvMsg`.

The HTTP adapter owns SSE and WebSocket protocol behavior. The gRPC adapter owns
gRPC metadata, peer, compression, status, and trailer behavior. Neither changes
the common middleware ordering.

## Transport-Specific Middleware

The complete server order is:

```text
transport panic backstop (built in, not removable)
  -> server-wide middleware (server construction option)
    -> generated service-default middleware
      -> generated method middleware
        -> business handler
```

Transport-native layers keep their native position: HTTP `Filter` functions
wrap the router and therefore run outside the server-wide middleware, while
additional gRPC interceptors are chained after Forge's own and run inside it.

Examples of transport-native behavior include:

- raw HTTP request or response mutation, cookies, CORS, decompression, body
  limits, and connection control;
- gRPC peer credentials, native status details, compression, stream headers,
  and transport statistics.

These layers use `net/http` middleware, HTTP filters, or gRPC interceptors. They
are not forced through `UnaryMiddleware` or `StreamMiddleware` and are not
claimed to be portable.

### Server-Wide Middleware

Each transport server accepts common middleware as a construction option:
`WithMiddleware` and `WithStreamMiddleware` on the gRPC server, and
`WithMiddleware` on the HTTP server (an HTTP stream is created by the handler
itself, after this layer has run, so there is no server-wide stream lifecycle
to wrap) and on the message server. It exists for concerns that are
genuinely server-global — tracing, logging, metadata — where repeating one
slice in every service plan invites drift between services that must not
drift apart.

This does not reintroduce the Kratos server option it superficially resembles.
The Kratos layer was mutable while serving: middleware lived in a slice the
server re-read and re-chained on the request path, and `Use`-style
registration could interleave with in-flight requests — a data race, and a
request-time composition cost. Forge's server-wide layer has neither property:

- The option only appends during `NewServer`; the chain is composed exactly
  once, before the server can accept a request, into an immutable closed
  handler. No server exposes a post-construction registration surface — not
  even a guarded one, because a method that usually errors is an API that
  teaches the wrong model.
- What travels per request is only the continuation of that request, passed
  through the request context into the pre-composed chain's terminal handler.
  No slice is read, merged, or re-chained on the request path.
- Nil middleware and nil-returning middleware fail composition at
  construction; the failure is reported by `Start`/`Endpoint`, the same
  surface that reports a bad listener, so a misconfigured server never serves.

Selector-based server middleware remains out of the core runtime: the
server-wide layer is deliberately method-blind, and anything method-aware
belongs in the generated plan, where selection is a compile-time field.

### Panic Backstop

Outside even the server-wide layer, each transport carries a built-in recover
at its outermost boundary — the gRPC unary and stream interceptors, the HTTP
server's root handler, the message server's binding wrapper. A panic anywhere
below is logged with its value and stack, and leaves the process as a generic
`KindInternal` error through the transport's normal error encoding; the panic
text never reaches the wire, per the disclosure model in `errors/public.go`.
The backstop is not configurable and not removable: it guarantees survival and
non-disclosure, nothing else. `middleware/recovery` remains the customization
layer — running inside the chains, it observes the panic first and may
classify it, so the backstop only sees panics nothing else handled.

## Registration and Failure Semantics

Generated wrapper constructors:

- validate and snapshot the plan;
- compose every handler exactly once;
- return errors rather than log and continue;
- identify the service and method when a middleware value or returned handler
  is nil;
- create no goroutines and own no application dependency lifecycle;
- are safe to use for independent application instances concurrently.

Wrapper construction completes before the transport registers the service.
There is no first-request initialization and no partially registered service
after a composition error.

Middleware request errors and panics retain the normal Forge error and
recovery boundaries. The framework never substitutes a no-op after a
construction failure.

## Request-Path Contract

The successful request or stream path must not:

- call `proto.GetExtension`;
- walk service or method descriptors;
- construct an operation name for middleware selection;
- match a selector, prefix, or regular expression;
- look up middleware by string or in a provider registry;
- merge middleware slices;
- rebuild wrapper closures.

Generated RPC fields provide compile-time selection. Slice snapshots and
composition happen during wrapper construction. Only the resulting closure
calls and application middleware work remain on the request path.

Operation identity may remain in context for logging, telemetry, and explicit
application behavior. The framework does not use it to discover the generated
middleware chain.

## Why Middleware Is Not Protobuf Policy

Arbitrary middleware names are Go application wiring, not portable API facts.
Putting names such as `Authenticate` in a public descriptor would:

- bind a language-neutral schema to exported Go identifier rules;
- turn Go refactoring into descriptor and generated API churn;
- force one generated interface to aggregate unrelated application packages;
- provide no parameters for method-specific authorization or limits;
- prove only that a method exists, not that HTTP and gRPC behavior matches;
- leave streaming lifecycle semantics undefined.

The unpublished fixed `policy/v1` prototype has the opposite problem: it makes
Forge own a closed list of authentication, authorization, validation,
audit, idempotency, rate, and budget capabilities. That list is not retained as
a compatibility layer.

Future portable annotations require an independent design showing that the
declared fact is meaningful to documentation and non-Go consumers. Runtime Go
middleware remains one possible implementation of such a fact, never its schema
identity.

## Migration

Forge selector middleware migrates to generated Go plans:

1. Resolve each selector to its exact protobuf method set.
2. Assign existing unary middleware values to the generated unary RPC fields.
3. Convert stream lifecycle behavior to `StreamMiddleware`; implement
   per-message behavior by decorating `ServerStream`.
4. Put raw HTTP or native gRPC behavior in the corresponding transport layer.
5. Build the generated HTTP and gRPC wrappers before standard registration.
6. Remove selector expressions and duplicated operation path strings.

The migration tool may use compiled descriptors to propose plan assignments. It
must not translate arbitrary regular expressions by textual guesswork. Migration
evidence compares the exact selected method set, order, context propagation,
reply, error, panic, and stream lifecycle behavior.

The inherited Forge gRPC stream implementation requires an explicit correction
during migration: the stream-handler chain must be invoked, not constructed and
discarded, and middleware must not be applied implicitly with different HTTP
and gRPC per-message semantics.

## Implementation Phases

### Phase 0: Remove unpublished schema experiments

- [x] Remove `forge/policy/v1` from the local API module, generated bindings,
  tests, and documentation.
- [x] Do not add `forge/middleware/v1`.
- [x] Re-run the API module's clean generation and local external-consumer gates.

### Phase 1: Runtime ABI

- [x] Rename the unary handler and middleware types to state their scope.
- [x] Add the minimal `ServerStream`, `StreamHandler`, and `StreamMiddleware`
  contracts.
- [x] Add composition, ordering, nil, context, error, panic, and stream-decoration
  tests.

### Phase 2: Generated plans and wrappers

- [x] Generate zero-value usable service plans with service-default and method
  fields.
- [x] Move the implemented plan and wrapper pass to
  `protoc-gen-go-middleware` as its sole owner.
- [x] Generate HTTP and gRPC wrappers consuming the same plan type without copying
  their wire bindings.
- [x] Snapshot and compose every operation during wrapper construction.
- [x] Preserve standard HTTP and `protoc-gen-go-grpc` registration entry points.

### Phase 3: Transport adoption

- [x] Route unary HTTP and gRPC calls through their precomposed wrappers.
- [x] Route all streaming shapes through one lifecycle stream chain.
- [x] Keep HTTP-native and gRPC-native middleware outside the common plan.
- [x] Remove selector lookup and dynamic chain construction from generated paths.

### Phase 4: Migration and evidence

- [x] Add mechanically precise Forge migration documentation.
- [x] Add a local external consumer using unary, server-streaming, client-streaming, and
  bidirectional methods over HTTP and gRPC.
- [x] Record steady-state middleware dispatch benchmarks.
- [x] Update compatibility documentation with the implemented API removal.
- [ ] After the first release, run the external consumer against published module
  versions without a repository-relative `replace`.

## Validation Contract

Required validation includes:

- deterministic generator golden tests for empty, default-only, method-only,
  and combined plans;
- Go compile tests for generated RPC fields and both transport wrappers;
- unary order, context, reply, error, panic, and nil-handler tests;
- all four RPC-shape tests over HTTP and gRPC;
- stream lifecycle, cancellation, EOF, half-close, terminal-error, SendMsg, and
  RecvMsg decoration tests, including server-streaming initial requests;
- tests proving shared plans have the same common order on HTTP and gRPC;
- tests proving different per-transport plans and native middleware remain
  possible;
- race tests for plan reuse, concurrent wrapper construction, and concurrent
  requests after construction;
- generated-source assertions proving no hook registry, selector, descriptor
  read, operation string dispatch, or first-request composition exists;
- external-consumer tests without `go.work` or repository-relative `replace`;
- benchmarks separating construction cost, framework dispatch, middleware
  business work, and transport-native work.

## Definition of Done

This work is complete only when:

- no public Proto schema declares middleware names or the unpublished fixed
  policy model;
- generated Go plans select middleware through RPC fields, not strings;
- users compose ordinary middleware functions without implementing a generated
  aggregate interface;
- unary and stream middleware have separate, documented contracts;
- one plan type can be reused by HTTP and gRPC without forcing reuse;
- native HTTP and gRPC capabilities remain available outside the common plan;
- all stream shapes have one tested lifecycle chain and explicit per-message
  decoration;
- wrapper construction snapshots and precomposes every handler before
  registration;
- the steady-state path performs no middleware discovery or chain construction;
- migration, external-consumer, correctness, race, and performance evidence is
  recorded.
