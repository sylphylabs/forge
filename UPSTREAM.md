# Upstream Policy

Forge is independently maintained and is not affiliated with or endorsed by the go-kratos maintainers.

## Baseline

- Upstream repository: `https://github.com/go-kratos/kratos.git`
- Upstream branch: `main`
- Initial commit: `668db92c2c001e9552594ba5a8aede8456af6d7e`
- Initial upstream release line: Kratos v3
- Forge module: `github.com/sylphylabs/forge`
- Initial Forge release line: v0

The complete upstream commit history is preserved. Upstream semantic-version tags are intentionally not copied into the Forge repository.

## Synchronization

The `upstream` remote is fetch-only and configured not to fetch tags. Upstream changes are reviewed and selected individually; Forge does not promise continuous merge compatibility with Kratos.

Security fixes and correctness fixes receive priority. Compatibility layers, dependency additions, and changes that conflict with the Forge architecture are not merged automatically.

Use the upstream commit hash in merge or cherry-pick descriptions so provenance remains auditable.

Review outcomes and local adoption commits are recorded in
[`docs/upstream-adoptions.md`](docs/upstream-adoptions.md).

Current user-visible differences and migration impact are recorded in
[`COMPATIBILITY.md`](COMPATIBILITY.md). The adoption ledger may discuss pending
work; the compatibility document records only accepted and validated behavior.
