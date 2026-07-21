# Upstream Policy

OpenKratos is independently maintained and is not affiliated with or endorsed by the go-kratos maintainers.

## Baseline

- Upstream repository: `https://github.com/go-kratos/kratos.git`
- Upstream branch: `main`
- Initial commit: `668db92c2c001e9552594ba5a8aede8456af6d7e`
- Initial upstream release line: Kratos v3
- OpenKratos module: `github.com/openkratos/kratos`
- Initial OpenKratos release line: v0

The complete upstream commit history is preserved. Upstream semantic-version tags are intentionally not copied into the OpenKratos repository.

## Synchronization

The `upstream` remote is fetch-only and configured not to fetch tags. Upstream changes are reviewed and selected individually; OpenKratos does not promise continuous merge compatibility with Kratos.

Security fixes and correctness fixes receive priority. Compatibility layers, dependency additions, and changes that conflict with the OpenKratos architecture are not merged automatically.

Use the upstream commit hash in merge or cherry-pick descriptions so provenance remains auditable.

Review outcomes and local adoption commits are recorded in
[`docs/upstream-adoptions.md`](docs/upstream-adoptions.md).
