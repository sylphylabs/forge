// Package selector provides middleware that applies other middleware
// conditionally, by matching the operation of the call in flight.
//
// [Client], [ServerStream], and [ClientStream] each start a builder that
// wraps the given middleware; Prefix, Path, Regex, and Match narrow which
// operations it runs for, and Build validates and returns the composed
// middleware. Calls that do not match pass through untouched.
//
// The operation string is matched as an opaque label — the same value
// [transport.Transporter.Operation] reports. Prefer attaching per-method
// middleware through the generated plan when the methods are yours (see
// docs/agent/middleware.md); this package covers the cases the plan cannot,
// such as client-side selection and patterns spanning services.
package selector
