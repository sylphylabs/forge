# Cross-service tests

Two Forge services in containers, so the error contract is exercised across
real processes, sockets, and serialization.

```sh
cd internal/e2e
FORGE_E2E=1 go test ./...
```

Without `FORGE_E2E` every test skips, so an ordinary `go test ./...` at the
repository root stays fast and needs no Docker.

Run this suite **on its own**. It builds two images and starts two containers;
combining that with a repository-wide `-race` sweep puts a Go build, a
race-instrumented binary, and the containers on the machine at once, which has
exhausted memory on a laptop.
