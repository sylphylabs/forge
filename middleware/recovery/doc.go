// Package recovery provides middleware that recovers a panicking handler,
// logs the panic value with its stack, and converts the panic into an error
// the transport can serve.
//
// [Recovery] wraps unary handlers, [Stream] wraps stream lifecycles. By
// default a recovered panic becomes [ErrUnknownRequest] (KindInternal);
// [WithHandler] substitutes a classifier that can inspect the panic value
// and return something more specific, and [WithLogger] redirects the log
// record.
//
// Every transport already carries a built-in, non-removable panic backstop
// outside all middleware, so this package is not what keeps the process
// alive. It is the customization layer: it runs inside the chains, so it
// observes the panic first and decides what error the caller sees, where the
// backstop only guarantees survival and non-disclosure. See
// docs/agent/middleware.md.
package recovery
