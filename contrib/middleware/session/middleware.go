package session

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/sylphylabs/forge/auth"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// DefaultCookieName is the header value used to carry the session ID when no
// other name is configured.
const DefaultCookieName = "session_id"

// IDExtractor pulls the session ID out of a request header. Returning an empty
// string means the request carried no credential.
type IDExtractor func(transport.Header) string

// Option configures the session middleware.
type Option func(*options)

type options struct {
	store   Store
	extract IDExtractor
	now     func() time.Time
}

// WithStore sets the Store holding sessions. It is required.
func WithStore(store Store) Option {
	return func(o *options) {
		o.store = store
	}
}

// WithIDExtractor replaces how the session ID is read from the request. The
// default reads the DefaultCookieName cookie, falling back to a header of the
// same name so that non-HTTP transports work without extra configuration.
func WithIDExtractor(extract IDExtractor) Option {
	return func(o *options) {
		o.extract = extract
	}
}

// WithClock replaces the clock used for expiry checks. It exists for tests.
func WithClock(now func() time.Time) Option {
	return func(o *options) {
		o.now = now
	}
}

// FromCookie returns an IDExtractor reading the named cookie, falling back to a
// header of the same name. The fallback is what lets gRPC and other
// metadata-based transports present the same credential.
func FromCookie(name string) IDExtractor {
	return func(header transport.Header) string {
		if id := cookieValue(header.Get("Cookie"), name); id != "" {
			return id
		}
		return header.Get(name)
	}
}

// FromHeader returns an IDExtractor reading the named header only.
func FromHeader(name string) IDExtractor {
	return func(header transport.Header) string {
		return header.Get(name)
	}
}

// cookieValue finds a cookie by name in a Cookie header value.
func cookieValue(cookieHeader, name string) string {
	if cookieHeader == "" {
		return ""
	}
	// http.ParseCookie handles quoting and spacing so this package does not
	// have to restate the grammar.
	cookies, err := http.ParseCookie(cookieHeader)
	if err != nil {
		return ""
	}
	for _, cookie := range cookies {
		if strings.EqualFold(cookie.Name, name) {
			return cookie.Value
		}
	}
	return ""
}

// Server authenticates a request by its session and publishes the caller as an
// auth.Principal.
//
// A request without a credential, or with one naming no live session, is
// rejected: the handler does not run. Use middleware/selector to exempt the
// operations that must stay reachable unauthenticated, such as login.
func Server(opts ...Option) middleware.UnaryMiddleware {
	o := newOptions(opts...)
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			ctx, err := o.authenticate(ctx)
			if err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}
	}
}

// ServerStream authenticates a stream by its session, once, when the stream
// opens. A session that expires mid-stream does not interrupt it: the record is
// read at the start, matching how metadata is read there.
func ServerStream(opts ...Option) middleware.StreamMiddleware {
	o := newOptions(opts...)
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			ctx, err := o.authenticate(stream.Context())
			if err != nil {
				return err
			}
			return handler(request, contextStream{ServerStream: stream, ctx: ctx})
		}
	}
}

func newOptions(opts ...Option) *options {
	o := &options{
		extract: FromCookie(DefaultCookieName),
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// authenticate resolves the session named by the request and returns a context
// carrying it and the resulting Principal.
func (o *options) authenticate(ctx context.Context) (context.Context, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok {
		return nil, ErrWrongContext
	}
	if o.store == nil {
		return nil, ErrMissingStore
	}

	id := o.extract(tr.RequestHeader())
	if id == "" {
		return nil, ErrMissingSessionID
	}

	s, err := o.store.Load(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	if s == nil {
		return nil, ErrSessionNotFound
	}
	if s.Expired(o.now()) {
		return nil, ErrSessionExpired
	}

	ctx = NewContext(ctx, s)
	return auth.NewContext(ctx, Principal{Session: s}), nil
}

// contextStream overrides the context of the stream it embeds.
type contextStream struct {
	middleware.ServerStream
	ctx context.Context
}

func (s contextStream) Context() context.Context { return s.ctx }
