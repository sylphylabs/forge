# Contributing to OpenKratos

OpenKratos accepts focused bug fixes, performance work, standard-library
modernization, and carefully justified compatibility changes. It is maintained
independently from `go-kratos/kratos`.

## Before Starting

1. Search existing issues and pull requests.
2. Read [`COMPATIBILITY.md`](COMPATIBILITY.md) for current behavior and
   [`UPSTREAM.md`](UPSTREAM.md) for the fork policy.
3. For an upstream change, check
   [`docs/upstream-adoptions.md`](docs/upstream-adoptions.md) before importing
   code.
4. Open a design proposal before implementing a new public abstraction or a
   broad compatibility break.

Use the issue templates. A bug report needs a minimal reproduction and exact
OpenKratos commit or module version. A proposal must define compatibility,
migration, and validation before implementation.

## Design Expectations

- Prefer the Go standard library when it meets the required contract.
- Keep behavior explicit. Avoid global source rewriting, hidden filesystem
  mutation, interactive build steps, and import-only side effects.
- Preserve Go conventions: small interfaces, explicit errors, useful zero
  values, and context propagation.
- Do not add a dependency without comparing its maintenance cost, API surface,
  performance, and standard-library alternatives.
- Do not preserve upstream behavior automatically when a smaller breaking
  design is clearer for the v0 release line.

## Development

OpenKratos requires Go 1.27. The repository contains nested Go modules, so a
root-only test does not cover the entire tree.

```shell
./hack/tools.sh tidy
./hack/tools.sh test
make lint
```

See [`DEVELOPMENT.md`](DEVELOPMENT.md) for the module matrix, RC toolchain
setup, and focused benchmarks.

Tests should be deterministic and offline unless they are explicitly marked as
integration tests. Concurrency changes require race coverage. Performance
changes require a controlled before-and-after baseline and repeated `benchstat`
analysis under the rules in
[`docs/design/performance.md`](docs/design/performance.md).

## Generated Code

Change protobuf sources and generator configuration, then regenerate. Do not
edit raw protobuf descriptors or apply global text replacement to generated
`.pb.go` files.

Keep generator versions pinned. Include generated output in the same change as
its source when the repository already tracks that output.

## Compatibility Documentation

A pull request must update the canonical `COMPATIBILITY.md` and its
`COMPATIBILITY_zh.md` translation when it changes any of the following:

- public Go API or module path;
- default runtime behavior;
- HTTP, gRPC, protobuf, or JSON wire behavior;
- generated code contract;
- command or development workflow;
- minimum Go version or supported platform.

Add an executable migration step under `docs/migration/` for a breaking change.
Record the exact upstream PR, issue, and source commit in the adoption ledger
when the work originates upstream.

## Commits and Pull Requests

Use Conventional Commit subjects:

```text
<type>[optional scope]: <imperative description>
```

Common types are `fix`, `feat`, `perf`, `deps`, `docs`, `refactor`, `test`,
`chore`, and `ci`. Add `!` and a `BREAKING CHANGE:` footer when appropriate.

Keep a pull request focused. Its description must state the problem, design,
compatibility impact, validation commands, and any external service required by
tests. Preserve upstream authorship and source provenance when adopting code.

## Security

Do not report vulnerabilities in a public issue. Follow
[`SECURITY.md`](SECURITY.md) and use the repository's private security advisory
form.
