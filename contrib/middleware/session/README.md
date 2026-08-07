# Session Middleware for Forge

Authenticates a caller by a server-side session and publishes the caller as an
[`auth.Principal`](../../../auth).

Unlike a JWT, a session can be revoked: the server owns the record rather than
merely verifying a signature.

## Bring your own Store

This module defines the contract and the middleware. It ships **no Store**.

Persistence is the application's choice, and a default in-memory store would
work in tests and then silently break the first time a service ran more than
one replica — users would be logged out at random depending on which pod served
them. Making the dependency explicit is the point.

```go
type Store interface {
	Load(ctx context.Context, id string) (*Session, error)
	Save(ctx context.Context, s *Session) error
	Delete(ctx context.Context, id string) error
}
```

Return `session.ErrNotFound` from `Load` when no record matches; the middleware
translates it to an unauthorized error. Any other error propagates unchanged, so
a store outage is not reported as an authentication failure.

`Delete` on an absent session must succeed, so that logout is idempotent.

## Usage

Middleware is bound through the generated per-service plan:

```go
import (
	"github.com/sylphylabs/forge/contrib/middleware/session"
	pb "example.com/api/auth/v1"
)

plan := pb.AuthServiceMiddleware{
	Unary: []middleware.UnaryMiddleware{
		session.Server(session.WithStore(myStore)),
	},
}
srv, err := pb.WrapAuthServiceHTTPServer(service, plan)
```

Reading the caller in a handler:

```go
subject := auth.Subject(ctx)              // just who it is
s, ok := session.FromContext(ctx)         // the whole record, when needed
```

Streaming methods use `session.ServerStream(...)` in the plan's `Stream` field.
The session is resolved once, when the stream opens; a session that expires
mid-stream does not interrupt it.

### Leaving login reachable

The middleware rejects any request without a live session, so do not apply it
to the operations that must stay open. The generated plan is per-method, so
list it under `Methods` rather than `Unary`:

```go
plan := pb.AuthServiceMiddleware{
	Methods: pb.AuthServiceMethodMiddleware{
		// Login and Refresh are deliberately absent.
		Logout:  []middleware.UnaryMiddleware{authenticated},
		GetUser: []middleware.UnaryMiddleware{authenticated},
	},
}
```

`middleware/selector` can express the same thing by operation pattern when a
service has too many methods to list.

## Where the credential comes from

By default the session ID is read from the `session_id` cookie, falling back to
a header of the same name. The fallback is what lets gRPC and other
metadata-based transports present the same credential without extra
configuration.

```go
session.WithIDExtractor(session.FromHeader("X-Session"))  // header only
session.WithIDExtractor(session.FromCookie("sid"))        // cookie, then header
```

The contract names no transport on purpose. Forge middleware runs above
`transport.Transporter`, and gRPC, message, and MCP have no `*http.Request` —
which is why this package does not follow `gorilla/sessions`, whose `Store`
takes `(*http.Request, http.ResponseWriter)` and is therefore HTTP-only.

## Writing the session

Creating a session at login and clearing it at logout is application code: call
`Store.Save` and `Store.Delete` directly. This middleware only reads.

The ID **must be unguessable** — possessing it is what authenticates the caller.
Generate it from a cryptographic source, for example `uuid.NewV4()` or
`crypto/rand`.

Setting the response cookie is likewise the application's job, because the
attributes depend on deployment. Use `Secure`, `HttpOnly`, and an appropriate
`SameSite` value.
