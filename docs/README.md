# Forge Documentation

## Usage Guides

Task-oriented, example-first guides. Written for coding agents, and equally
usable by people who want the rules without the rationale. [AGENTS.md](../AGENTS.md)
is the entry point.

- [Errors](agent/errors.md)
- [Observability](agent/observability.md)
- [Middleware](agent/middleware.md)
- [Application, transports, and config](agent/application.md)

## Current Behavior

- [Compatibility contract](../COMPATIBILITY.md)
- [Compatibility contract (Simplified Chinese)](../COMPATIBILITY_zh.md)
- [Migration from Kratos v3](migration/kratos-to-forge.md)
- [Migration from Kratos v3 (Simplified Chinese)](migration/kratos-to-forge_zh.md)

## Decisions

Architecture decision records. A design document says how something works; an
ADR says why it was chosen and what was rejected, and stays in place once
accepted so that a later reader can tell a deliberate constraint from an
accident. [ADR-0001](adr/0001-adr-triggers.md) states when one is required.

- [ADR-0001: When an ADR is required](adr/0001-adr-triggers.md)
- [ADR-0002: Transporter describes what transports share](adr/0002-transporter-describes-transport-commonality.md)
- [ADR-0003: MCP reaches framework middleware](adr/0003-mcp-bridges-framework-middleware.md)
- [ADR-0004: Principal in the core, policy in the application](adr/0004-principal-in-core-tenant-in-application.md)
- [ADR-0005: Session defines a contract, not a store](adr/0005-session-defines-contract-not-store.md)
- [ADR-0006: Errors classify by kind, not transport code](adr/0006-errors-kind-over-transport-code.md)
- [ADR-0007: errors is a zero-dependency leaf](adr/0007-errors-zero-dependency-leaf.md)
- [ADR-0008: Retryability needs delivery evidence](adr/0008-retryability-needs-delivery-evidence.md)
- [ADR-0009: Message transport parity with HTTP and gRPC](adr/0009-message-transport-parity.md)
- [ADR-0010: A destination is adapter-defined](adr/0010-destination-is-adapter-defined.md)

## Design and Evidence

- [Errors](design/errors.md)
- [Runtime modernization](design/runtime-modernization.md)
- [Application lifecycle](design/application-lifecycle.md)
- [Config lifecycle](design/config-lifecycle.md)
- [HTTP request-path optimization plan](design/http-request-path-optimization.md)
- [Public Protobuf API module](design/public-protobuf-api-module.md)
- [Forge Protobuf generation](design/protobuf-generation.md)
- [OpenAPI 3.2 generation](design/openapi-3.2.md)
- [Generated middleware wiring](design/generated-middleware.md)
- [Asynchronous message transport](design/message-transport.md)
- [OpenTelemetry metrics contract](design/otel-metrics.md)
- [Performance modernization](design/performance.md)
- [Google HTTP transcoding conformance](design/google-http-transcoding.md)
- [HTTP API standards reference (Simplified Chinese)](design/http-api-standards_zh.md)
- [Selector benchmark: July 22, 2026](benchmarks/selectors-2026-07-22.md)

## Maintenance

- [Upstream policy](../UPSTREAM.md)
- [Upstream adoption ledger](upstream-adoptions.md)
- [Development and validation](../DEVELOPMENT.md)
- [Contribution guide](../CONTRIBUTING.md)
- [Security policy](../SECURITY.md)

## Archive

- [Kratos v2 design archive](design/forge-v2.md)

Archive documents preserve upstream design history. They do not define current
Forge behavior.
