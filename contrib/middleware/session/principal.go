package session

import (
	"context"

	"github.com/sylphylabs/forge/auth"
)

var _ auth.Principal = Principal{}

// Principal is the auth.Principal backed by a server-held session.
//
// It exposes the Session so callers needing session data can reach it, while
// callers that only need to know who is calling depend on auth.Principal
// instead.
type Principal struct {
	Session *Session
}

// Subject returns the subject the session belongs to.
func (p Principal) Subject() string {
	if p.Session == nil {
		return ""
	}
	return p.Session.Subject
}

type sessionKey struct{}

// NewContext returns a context carrying s.
//
// Only the session middleware, or code that has itself verified the credential
// against the Store, may call this. A Session MUST NOT be reconstructed from
// inbound headers: possessing the ID is what authenticates the caller, so
// trusting a client-supplied record would let a caller name itself.
func NewContext(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// FromContext returns the Session stored in ctx, if any.
func FromContext(ctx context.Context) (s *Session, ok bool) {
	s, ok = ctx.Value(sessionKey{}).(*Session)
	return
}
