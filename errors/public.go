package errors

import "maps"

// Public is the data an error may disclose outside its process.
//
// It is the only input a transport accepts, so what crosses a boundary is
// decided by construction rather than by inspection. Everything a caller
// declared — the message, the metadata, the violations — is here; the cause
// chain and any wrapped Go value are not, because there is no way to declare
// those safe.
//
// The alternative Forge used to ship read the Kind and guessed. That model
// could not observe what it needed to: a KindNotFound message may name a
// tenant, a violation description may quote a driver's error, and neither is
// visible to a rule written in terms of Kind. Only the caller who wrote the
// field knows whether it is public, and calling Msg, Meta, WithMetadata, or
// adding a Violation is how they say so.
type Public struct {
	Kind       Kind
	Domain     string
	Reason     string
	Message    string
	Metadata   map[string]string
	TraceID    string
	Violations []Violation
}

// PublicOf returns the disclosable data of any error.
//
// The result owns its maps and slices, so a transport cannot mutate the error
// it came from, and never contains a cause, a formatted error string, or a
// wrapped value. An error that did not originate from this package discloses
// only KindUnknown: its text was written for an operator, not for a caller, and
// a transport supplies its own generic message instead.
//
// Disclosure is gated on declaration. An error speaks for a service's contract
// only when its identity was declared through [MustDefine] — which is how
// generated *_errors.pb.go files and deliberate framework sentinels come into
// being. A locally produced error whose identity was never declared — an [Of]
// product, or an ad-hoc WithDomain/WithReason pair — projects as an internal
// failure carrying only its trace ID: its Kind, reason, message, metadata, and
// violations were assembled for in-process use, and letting them cross would
// both leak internal taxonomy and freeze accidental reasons into public API.
// The original classification is not lost to the operator: logging and metrics
// observe the error itself, before projection.
//
// A remote error is exempt: its data arrived over the wire from a peer that
// already chose to disclose it, so passing it on discloses nothing new. That
// keeps proxied statuses and health-check semantics intact.
func PublicOf(err error) Public {
	if err == nil {
		return Public{}
	}
	e := asError(err)
	if e == nil {
		return Public{}
	}
	if e.undisclosed {
		// Undisclose is a deliberate verdict that overrides every other rule,
		// including the remote pass-through: it exists precisely for identities
		// whose disclosure would otherwise be legal but is not wanted here.
		return Public{Kind: KindInternal, TraceID: e.trace}
	}
	if !e.remote && !isContract(e.domain, e.reason) {
		return Public{Kind: KindInternal, TraceID: e.trace}
	}
	return Public{
		Kind:       e.kind,
		Domain:     e.domain,
		Reason:     e.reason,
		Message:    e.msg,
		Metadata:   maps.Clone(e.meta),
		TraceID:    e.trace,
		Violations: append([]Violation(nil), e.violations...),
	}
}

// FromPublic reconstructs an error from data received over the wire.
//
// It is the inverse of [PublicOf]: one type describes what may leave a process
// and what arrives from one, because those are the same set of facts seen from
// two sides.
//
// The result is marked remote: it describes a failure in another process, so it
// carries no cause and [errors.As] will not reach a local type through it.
//
// An identity is accepted only as a complete pair. A domain without a reason,
// or a reason without a domain, is not an identity a sentinel can be matched
// against, and keeping half of one would let unrelated failures compare equal.
func FromPublic(p Public) *Error {
	e := &Error{
		kind:   p.Kind,
		msg:    p.Message,
		meta:   maps.Clone(p.Metadata),
		trace:  p.TraceID,
		remote: true,
	}
	if p.Domain != "" && p.Reason != "" {
		e.domain = p.Domain
		e.reason = p.Reason
	}
	if len(p.Violations) > 0 {
		e.violations = append([]Violation(nil), p.Violations...)
	}
	return e
}

// Undisclose returns err with its public data marked as not disclosable.
//
// [PublicOf] projects the result as an internal failure carrying only its
// trace ID, regardless of every other disclosure rule — a declared contract
// identity and even a remote pass-through are overridden, because Undisclose
// is a deliberate verdict about this occurrence, not a property of the
// identity. Everything else about the error is untouched: [Is] still matches
// its sentinel, accessors still return its fields, and the original error
// remains reachable through [Unwrap], so logging and metrics observe the real
// failure before the projection hides it.
//
// It is the mechanism behind strict throws assertion: a generated wrapper
// that catches an undeclared contract identity leaving a method does not
// rewrite the error — classification stays observable in-process — it marks
// the error, and the single disclosure gate does the rest.
func Undisclose(err error) error {
	if err == nil {
		return nil
	}
	c := FromError(err).clone()
	c.undisclosed = true
	// Keep the complete original chain reachable: FromError selects the first
	// *Error in the chain, and any wrapping outside it must stay diagnosable.
	c.cause = err
	return c
}

// IsUndisclosed reports whether err carries the [Undisclose] marker.
func IsUndisclosed(err error) bool {
	e := asError(err)
	return e != nil && e.undisclosed
}

// IsContract reports whether the identity pair was declared through
// [MustDefine] in this process.
//
// It answers "may this identity disclose itself through [PublicOf]?" — the
// same registry consultation the projection performs. Runtime assertion uses
// it to separate an undeclared contract identity, which would leave the
// process fully disclosed, from an anonymous local failure, which projects as
// an internal error anyway.
func IsContract(domain, reason string) bool {
	return isContract(domain, reason)
}

// asError returns err as an *Error, or nil when it did not originate here.
func asError(err error) *Error {
	var e *Error
	if As(err, &e) {
		return e
	}
	return nil
}
