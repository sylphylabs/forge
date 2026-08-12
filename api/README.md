# Forge API

This repository is the canonical source for portable Forge Protobuf
contracts. It is currently a local release prototype and has not been
published to GitHub or the Buf Schema Registry.

## Modules

- Go: `github.com/sylphylabs/forge/api`
- Buf: `buf.build/forge/api`

Public schemas live below `proto/forge`. Generated Go bindings are
committed under the corresponding module-root package so Go consumers do not
need Protobuf tooling.

## Contracts

- `sylphy/errors/v1/errors.proto` defines the error Kind vocabulary and the
  enum annotations for declaring contract errors.
- `sylphy/message/v1/message.proto` defines the asynchronous subscription
  annotation. `subscribe` binds one method to a `Subscription`, whose
  `destination` is the contract default that a deployment overrides at
  registration time.

Middleware wiring belongs in generated Go service plans rather than Protobuf.
The API module publishes no operation-policy or middleware-naming schema.

There is no Protobuf error envelope: HTTP carries errors as RFC 9457 Problem
Details and gRPC as a `google.rpc.Status` with standard details, so a third
canonical representation would only force both transports through an
unrelated one.

Runtime middleware, credentials, provider configuration, concrete limits, and
deployment secrets do not belong in this module.

## Local Validation

The Buf CLI is executed at the version pinned in the Makefile without adding
it to the public Go module dependency graph.

```shell
make all
git diff --exit-code
```

`testdata/consumer` is a separate Go module that compiles a business schema
against the local API module. Its local `replace` is intentionally limited to
the unpublished prototype; release validation must remove the replacement and
use a tagged API version.

No command in this repository publishes a Git tag, Go module, or BSR module.
