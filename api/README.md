# OpenKratos API

This repository is the canonical source for portable OpenKratos Protobuf
contracts. It is currently a local release prototype and has not been
published to GitHub or the Buf Schema Registry.

## Modules

- Go: `github.com/openkratos/api`
- Buf: `buf.build/openkratos/api`

Public schemas live below `proto/openkratos`. Generated Go bindings are
committed under the corresponding module-root package so Go consumers do not
need Protobuf tooling.

## Contracts

- `openkratos/errors/v1/errors.proto` defines the portable error status and
  enum annotations.

Middleware wiring belongs in generated Go service plans rather than Protobuf.
The API module publishes no operation-policy or middleware-naming schema.

`openkratos.errors.v1.Status` deliberately preserves the OpenKratos error
envelope `{code, reason, message, metadata}`. It is not replaced by
`google.rpc.Status`, which cannot represent the stable `reason` and string
metadata map as the same contract.

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
