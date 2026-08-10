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
func PublicOf(err error) Public {
	if err == nil {
		return Public{}
	}
	e := asError(err)
	if e == nil {
		return Public{}
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

// asError returns err as an *Error, or nil when it did not originate here.
func asError(err error) *Error {
	var e *Error
	if As(err, &e) {
		return e
	}
	return nil
}
