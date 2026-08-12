// Package log builds structured loggers on the standard library's log/slog.
//
// Forge defines no logger interface of its own: components accept and return
// *[log/slog.Logger]. [NewHandler] builds a composed handler with the
// package's defaults — text or JSON encoding ([WithFormat]), a minimum level
// ([WithLevel]), context-attribute extraction, and key redaction
// ([WithFilter], [WithFilterKey]) — and [NewLogger] wraps any handler,
// including one from another package, in the same decorators.
//
// [ContextWithAttrs] attaches attributes to a context; every record logged
// with that context through a composed handler carries them, which is how
// per-request values such as trace IDs reach log output without threading a
// logger through every call. [SetDefault] installs a logger as both this
// package's and slog's process default; the package-level helpers ([With],
// [WithGroup], [Handler], [Enabled]) mirror the slog API on that default.
//
// The OpenTelemetry bridge lives in the separate contrib/otel/log module;
// see docs/agent/observability.md for wiring logs into traces.
package log
