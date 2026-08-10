// Package session authenticates a caller by a server-side session.
//
// The credential travels in a request header — a cookie over HTTP, ordinary
// metadata over gRPC — and names a record held by a Store. That indirection is
// the point: unlike a JWT, a session can be revoked, because the server owns
// the record rather than merely verifying a signature.
//
// This package defines the contract and the middleware. It deliberately ships
// no Store: the choice of Redis, a database, or anything else belongs to the
// application, and a default in-memory store would silently break the moment a
// service ran more than one replica.
package session

import (
	"context"
	"errors"
	"time"

	forgeerrors "github.com/sylphylabs/forge/errors"
)

// domain namespaces the reasons this middleware raises.
const domain = "session.middleware.forge.sylphylabs.io"

var (
	// ErrMissingSessionID reports a request that carried no session credential.
	ErrMissingSessionID = forgeerrors.MustDefine(forgeerrors.KindUnauthenticated, domain, "MISSING_SESSION_ID").Msg("Session ID is missing")
	// ErrSessionNotFound reports a credential naming no live session.
	ErrSessionNotFound = forgeerrors.MustDefine(forgeerrors.KindUnauthenticated, domain, "SESSION_NOT_FOUND").Msg("Session not found")
	// ErrSessionExpired reports a session past its expiry.
	ErrSessionExpired = forgeerrors.MustDefine(forgeerrors.KindUnauthenticated, domain, "SESSION_EXPIRED").Msg("Session has expired")
	// ErrMissingStore reports a middleware built without a Store.
	ErrMissingStore = forgeerrors.MustDefine(forgeerrors.KindUnauthenticated, domain, "MISSING_STORE").Msg("Session store is missing")
	// ErrWrongContext reports a context carrying no server transport.
	ErrWrongContext = forgeerrors.MustDefine(forgeerrors.KindUnauthenticated, domain, "WRONG_CONTEXT").Msg("Wrong context for middleware")

	// ErrNotFound is returned by a Store when no record matches the ID. The
	// middleware translates it to ErrSessionNotFound; a Store MUST NOT report a
	// missing record as an internal failure.
	ErrNotFound = errors.New("session: not found")
)

// Session is a server-held record identified by a credential the client
// presents on each request.
type Session struct {
	// ID names this session. A Store generates it; it MUST be unguessable,
	// because possessing it is what authenticates the caller.
	ID string
	// Subject is the caller this session belongs to, and becomes the Subject
	// of the auth.Principal the middleware publishes.
	Subject string
	// ExpiresAt bounds the session's life. The zero value means the Store
	// alone decides expiry, so a Store that does not expire records MUST set
	// it if sessions are to expire at all.
	ExpiresAt time.Time
	// Values carries application data. The framework never reads it.
	Values map[string]any
}

// Expired reports whether s is past its expiry at now. A zero ExpiresAt never
// expires by this check.
func (s *Session) Expired(now time.Time) bool {
	if s == nil {
		return true
	}
	if s.ExpiresAt.IsZero() {
		return false
	}
	return now.After(s.ExpiresAt)
}

// Store holds sessions.
//
// The signatures name no transport: Forge middleware runs above the
// transport.Transporter abstraction, and gRPC, message, and MCP have no
// *http.Request to pass. This is why the contract does not follow
// gorilla/sessions, whose Store takes (*http.Request, http.ResponseWriter).
//
// An implementation MUST be safe for concurrent use.
type Store interface {
	// Load returns the session with this ID, or ErrNotFound if none is live.
	Load(ctx context.Context, id string) (*Session, error)
	// Save persists s, creating or replacing the record under s.ID.
	Save(ctx context.Context, s *Session) error
	// Delete removes the session with this ID. Deleting an absent session is
	// not an error, so that logout is idempotent.
	Delete(ctx context.Context, id string) error
}
