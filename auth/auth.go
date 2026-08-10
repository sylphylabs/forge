// Package auth carries the identity of the caller in flight.
//
// It answers one question — who is calling — and deliberately answers no
// others. Tenancy is an application model rather than a transport property, so
// it belongs in application code.
package auth

import "context"

// Principal is the authenticated caller behind a request.
//
// The interface is deliberately minimal. An authentication middleware fills it
// and business code reads it, so JWT, mTLS, API keys, and session cookies can
// each supply one without agreeing on anything beyond a subject. Widening it
// later is additive; narrowing it is not.
//
// Implementations that carry richer credential detail should expose it through
// their own package rather than through this interface. Authorization — what a
// caller may do — is a separate concern and is not modeled here.
type Principal interface {
	// Subject identifies the caller: a user ID, a service account name, or a
	// certificate subject. It is opaque to the framework.
	Subject() string
}

// principal is the minimal Principal, sufficient for callers that carry
// nothing but a subject.
type principal struct {
	subject string
}

func (p principal) Subject() string { return p.subject }

// New returns a Principal for subject.
func New(subject string) Principal {
	return principal{subject: subject}
}

type principalKey struct{}

// NewContext returns a context carrying p.
//
// Only a trusted authentication middleware may call this. A Principal MUST NOT
// be reconstructed from inbound metadata or headers: those are attacker
// controlled, and treating them as identity would let a caller name itself.
func NewContext(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the Principal stored in ctx, if any.
func FromContext(ctx context.Context) (p Principal, ok bool) {
	p, ok = ctx.Value(principalKey{}).(Principal)
	return
}

// Subject returns the subject of the Principal in ctx, or the empty string
// when the request is unauthenticated. It suits logging and metrics, where an
// absent caller is not an error.
func Subject(ctx context.Context) string {
	if p, ok := FromContext(ctx); ok && p != nil {
		return p.Subject()
	}
	return ""
}
