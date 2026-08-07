package jwt

import (
	"github.com/golang-jwt/jwt/v5"

	"github.com/sylphylabs/forge/auth"
)

var _ auth.Principal = Principal{}

// Principal is the auth.Principal backed by a verified JWT.
//
// It exposes the embedded Claims so callers that need JWT-specific detail can
// reach it, while callers that only need to know who is calling can depend on
// auth.Principal instead. Server puts both this and the bare Claims in the
// context: the two are different levels of abstraction, not duplicates.
type Principal struct {
	Claims jwt.Claims
}

// Subject returns the "sub" claim, or the empty string when the token carries
// none. A JWT is valid without a subject, so an empty result is not an error.
func (p Principal) Subject() string {
	if p.Claims == nil {
		return ""
	}
	subject, err := p.Claims.GetSubject()
	if err != nil {
		return ""
	}
	return subject
}
