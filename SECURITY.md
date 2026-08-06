# Security Policy

## Supported Versions

Forge is currently unreleased on the v0 development line. There is no
stable version with a long-term security support commitment yet.

| Version | Supported |
| --- | --- |
| Current `main` development line | Best effort |
| Unreleased snapshots and older commits | No separate support guarantee |
| Upstream Kratos releases | Maintained by the upstream project |

Published support windows will be added here before the first stable release.

## Reporting a Vulnerability

Report vulnerabilities privately through the Forge repository's
[security advisory form](https://github.com/sylphylabs/forge/security/advisories/new).
Do not open a public issue or discussion before maintainers have assessed the
report.

Include:

- the affected Forge version or commit;
- a minimal reproduction or proof of concept;
- affected transports, modules, and deployment assumptions;
- expected impact and known mitigations;
- whether the same issue has been verified against upstream Kratos.

Forge-specific vulnerabilities should not be reported to upstream Kratos.
If independent verification shows that upstream is also affected, follow the
upstream project's private reporting policy separately and avoid public
disclosure until both projects have had time to respond.
