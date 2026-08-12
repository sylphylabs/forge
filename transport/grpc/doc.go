// Package grpc provides Forge's gRPC transport: a server wrapping
// google.golang.org/grpc with Forge's lifecycle and middleware contracts,
// and a client constructor that wires the same contracts into a
// *grpc.ClientConn.
//
// [NewServer] builds a [Server] from options — [WithAddress], server-wide
// [WithMiddleware] for unary methods and [WithStreamMiddleware] for
// streaming methods, TLS, health, and raw grpc.ServerOption passthrough via
// [WithOptions]. Generated services register on it directly; per-service and
// per-method middleware goes through the generated plan instead (see
// docs/agent/middleware.md). A built-in, non-removable panic backstop sits
// outside all middleware: a panic is logged and the client receives a
// generic internal error that never contains the panic text.
//
// [NewClient] returns a *grpc.ClientConn from [WithTarget],
// [WithClientMiddleware], [WithRequestTimeout], discovery/selector options,
// and raw grpc.DialOption passthrough via [WithDialOptions]. Remote statuses
// are converted into Forge errors — identity, metadata, violations, and
// trace intact — before application middleware observes them, on unary calls
// and on every stream operation alike (see docs/design/errors.md).
package grpc
