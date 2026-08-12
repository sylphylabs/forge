// Package validate provides server middleware that rejects invalid requests
// before the handler runs.
//
// [Validator] wraps unary handlers, [ValidatorStream] validates every
// received stream message. Both run a request's own Validate() error method
// when it has one, then every configured [ValidatorFunc] — the hook that
// plugs in schema validators such as protovalidate or AIP field-behavior
// checks without this package depending on them.
//
// A rejected request fails with [ErrValidation] (KindInvalidArgument), the
// validator's own error preserved as the wrapped cause. A validator whose
// error implements [FieldReporter] produces an aggregate error carrying one
// violation per failed field, so a client sees everything that was wrong
// rather than only the first (see docs/agent/errors.md).
package validate
