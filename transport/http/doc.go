// Package http provides Forge's HTTP transport: a server that generated
// service code registers routes on, and a client that generated code calls
// through.
//
// [NewServer] builds a [Server] from options — [WithAddress], server-wide
// [WithMiddleware], net/http-level [WithFilter], codec hooks, TLS — and
// serves the routes generated protoc-gen-go-http code registers with
// RegisterXxxHTTPServer. A built-in, non-removable panic backstop sits
// outside all middleware: a panic is logged and the client receives a
// generic internal error that never contains the panic text. [NewClient]
// builds a [Client] from [WithTarget], [WithClientMiddleware],
// [WithRequestTimeout], and discovery/selector options, and converts error
// responses into Forge errors before application middleware sees them.
//
// Errors cross this transport as RFC 9457 problem+json documents
// ([ProblemContentType]) whose members are the error's own vocabulary —
// kind, domain, reason, message, trace_id — projected by [StatusOf] and read
// back by the client under the rules in docs/design/errors.md. SSE and
// WebSocket streaming live here too; Google HTTP transcoding and the pprof
// and healthz handlers are subpackages.
//
// See docs/agent/application.md and docs/agent/middleware.md for the usage
// contracts.
package http
