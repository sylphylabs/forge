package jwt

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/sylphylabs/forge/auth"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func TestPrincipalSubject(t *testing.T) {
	tests := []struct {
		name   string
		claims jwt.Claims
		want   string
	}{
		{
			name:   "registered claims",
			claims: jwt.RegisteredClaims{Subject: "user-1"},
			want:   "user-1",
		},
		{
			name:   "map claims",
			claims: jwt.MapClaims{"sub": "user-2"},
			want:   "user-2",
		},
		{
			// A JWT is valid without a subject, so this is not an error.
			name:   "no subject",
			claims: jwt.RegisteredClaims{},
			want:   "",
		},
		{
			name:   "nil claims",
			claims: nil,
			want:   "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := (Principal{Claims: test.claims}).Subject(); got != test.want {
				t.Errorf("Subject() = %q, want %q", got, test.want)
			}
		})
	}
}

// The middleware must publish the caller through the framework abstraction, so
// business code need not know the credential is a JWT.
func TestServerPutsPrincipalInContext(t *testing.T) {
	const testKey = "testKey"

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.RegisteredClaims{
		Subject: "user-1",
	}).SignedString([]byte(testKey))
	if err != nil {
		t.Fatal(err)
	}

	ctx := transport.NewServerContext(t.Context(), &Transport{
		kind:      transport.KindHTTP,
		reqHeader: newTokenHeader(authorizationKey, "Bearer "+token),
	})

	var (
		gotSubject string
		gotClaims  jwt.Claims
	)
	next := func(ctx context.Context, _ any) (any, error) {
		gotSubject = auth.Subject(ctx)
		// The JWT-specific view stays available alongside the generic one.
		gotClaims, _ = FromContext(ctx)
		return nil, nil
	}

	handler := Server(func(*jwt.Token) (any, error) { return []byte(testKey), nil })(
		middleware.UnaryHandler(next),
	)
	if _, err := handler(ctx, nil); err != nil {
		t.Fatal(err)
	}

	if gotSubject != "user-1" {
		t.Errorf("auth.Subject() = %q, want %q", gotSubject, "user-1")
	}
	if gotClaims == nil {
		t.Error("jwt.FromContext() returned no claims; both views must be present")
	}
}

func TestServerPrincipalIsJWTPrincipal(t *testing.T) {
	const testKey = "testKey"

	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &CustomerClaims{
		Name:             "forge",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-1"},
	}).SignedString([]byte(testKey))
	if err != nil {
		t.Fatal(err)
	}

	ctx := transport.NewServerContext(t.Context(), &Transport{
		kind:      transport.KindHTTP,
		reqHeader: newTokenHeader(authorizationKey, "Bearer "+token),
	})

	var principal auth.Principal
	next := func(ctx context.Context, _ any) (any, error) {
		principal, _ = auth.FromContext(ctx)
		return nil, nil
	}

	handler := Server(
		func(*jwt.Token) (any, error) { return []byte(testKey), nil },
		WithClaims(func() jwt.Claims { return &CustomerClaims{} }),
	)(middleware.UnaryHandler(next))
	if _, err := handler(ctx, nil); err != nil {
		t.Fatal(err)
	}

	// Callers that need the custom claim can reach it through the concrete type.
	jwtPrincipal, ok := principal.(Principal)
	if !ok {
		t.Fatalf("principal type = %T, want jwt.Principal", principal)
	}
	customer, ok := jwtPrincipal.Claims.(*CustomerClaims)
	if !ok {
		t.Fatalf("claims type = %T, want *CustomerClaims", jwtPrincipal.Claims)
	}
	if customer.Name != "forge" {
		t.Errorf("Name = %q, want %q", customer.Name, "forge")
	}
}

func TestServerRejectedRequestHasNoPrincipal(t *testing.T) {
	ctx := transport.NewServerContext(t.Context(), &Transport{
		kind:      transport.KindHTTP,
		reqHeader: newTokenHeader(authorizationKey, "Bearer bad-token"),
	})

	called := false
	handler := Server(func(*jwt.Token) (any, error) { return []byte("testKey"), nil })(
		func(context.Context, any) (any, error) {
			called = true
			return nil, nil
		},
	)
	if _, err := handler(ctx, nil); err == nil {
		t.Fatal("Server() error = nil, want a rejection")
	}
	if called {
		t.Error("the handler ran despite the token being rejected")
	}
	if _, ok := auth.FromContext(ctx); ok {
		t.Error("a rejected request must not leave a Principal behind")
	}
}
