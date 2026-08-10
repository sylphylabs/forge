package session

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/sylphylabs/forge/auth"
	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// memStore is a Store for tests only. The package ships no Store precisely
// because an in-memory one breaks across replicas.
type memStore struct {
	mu       sync.Mutex
	sessions map[string]*Session
	loadErr  error
}

func newMemStore(sessions ...*Session) *memStore {
	s := &memStore{sessions: make(map[string]*Session, len(sessions))}
	for _, session := range sessions {
		s.sessions[session.ID] = session
	}
	return s
}

func (s *memStore) Load(_ context.Context, id string) (*Session, error) {
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[id]
	if !ok {
		return nil, ErrNotFound
	}
	return session, nil
}

func (s *memStore) Save(_ context.Context, session *Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.ID] = session
	return nil
}

func (s *memStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}

type headerCarrier http.Header

func (hc headerCarrier) Get(key string) string      { return http.Header(hc).Get(key) }
func (hc headerCarrier) Set(key, value string)      { http.Header(hc).Set(key, value) }
func (hc headerCarrier) Add(key, value string)      { http.Header(hc).Add(key, value) }
func (hc headerCarrier) Values(key string) []string { return http.Header(hc).Values(key) }

func (hc headerCarrier) Keys() []string {
	keys := make([]string, 0, len(hc))
	for k := range hc {
		keys = append(keys, k)
	}
	return keys
}

type testTransport struct {
	header transport.Header
}

func (tr *testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (tr *testTransport) Endpoint() string                { return "" }
func (tr *testTransport) Operation() string               { return "/test.Service/Method" }
func (tr *testTransport) RequestHeader() transport.Header { return tr.header }

func serverCtx(t *testing.T, header transport.Header) context.Context {
	t.Helper()
	return transport.NewServerContext(t.Context(), &testTransport{header: header})
}

func cookieHeader(value string) transport.Header {
	hc := headerCarrier{}
	hc.Set("Cookie", value)
	return hc
}

func TestServerAuthenticates(t *testing.T) {
	store := newMemStore(&Session{ID: "abc", Subject: "user-1"})
	ctx := serverCtx(t, cookieHeader(DefaultCookieName+"=abc"))

	var (
		gotSubject string
		gotSession *Session
	)
	handler := Server(WithStore(store))(func(ctx context.Context, _ any) (any, error) {
		gotSubject = auth.Subject(ctx)
		gotSession, _ = FromContext(ctx)
		return "reply", nil
	})

	reply, err := handler(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "reply" {
		t.Errorf("reply = %v, want %q", reply, "reply")
	}
	if gotSubject != "user-1" {
		t.Errorf("auth.Subject() = %q, want %q", gotSubject, "user-1")
	}
	if gotSession == nil || gotSession.ID != "abc" {
		t.Errorf("FromContext() = %v, want the loaded session", gotSession)
	}
}

func TestServerRejects(t *testing.T) {
	live := &Session{ID: "abc", Subject: "user-1"}
	expired := &Session{ID: "old", Subject: "user-1", ExpiresAt: time.Unix(1000, 0)}

	tests := []struct {
		name   string
		store  Store
		header transport.Header
		want   error
	}{
		{
			name:   "no credential",
			store:  newMemStore(live),
			header: headerCarrier{},
			want:   ErrMissingSessionID,
		},
		{
			name:   "unknown session",
			store:  newMemStore(live),
			header: cookieHeader(DefaultCookieName + "=nope"),
			want:   ErrSessionNotFound,
		},
		{
			name:   "expired session",
			store:  newMemStore(expired),
			header: cookieHeader(DefaultCookieName + "=old"),
			want:   ErrSessionExpired,
		},
		{
			name:   "no store",
			store:  nil,
			header: cookieHeader(DefaultCookieName + "=abc"),
			want:   ErrMissingStore,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := Server(
				WithStore(test.store),
				WithClock(func() time.Time { return time.Unix(2000, 0) }),
			)(func(context.Context, any) (any, error) {
				called = true
				return nil, nil
			})

			_, err := handler(serverCtx(t, test.header), nil)
			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			if called {
				t.Error("the handler ran despite the request being rejected")
			}
		})
	}
}

func TestServerWithoutTransport(t *testing.T) {
	handler := Server(WithStore(newMemStore()))(func(context.Context, any) (any, error) {
		return nil, nil
	})
	if _, err := handler(t.Context(), nil); !errors.Is(err, ErrWrongContext) {
		t.Errorf("error = %v, want %v", err, ErrWrongContext)
	}
}

// A Store failure is not an authentication failure and must not be reported as
// one: the caller may well be valid.
func TestServerPropagatesStoreError(t *testing.T) {
	want := errors.New("redis unreachable")
	store := newMemStore()
	store.loadErr = want

	handler := Server(WithStore(store))(func(context.Context, any) (any, error) {
		return nil, nil
	})

	_, err := handler(serverCtx(t, cookieHeader(DefaultCookieName+"=abc")), nil)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want it to wrap %v", err, want)
	}
	if forgeerrors.KindOf(err) == forgeerrors.KindUnauthenticated {
		t.Error("a store outage was reported as an authentication failure")
	}
}

func TestServerStreamAuthenticates(t *testing.T) {
	store := newMemStore(&Session{ID: "abc", Subject: "user-1"})
	ctx := serverCtx(t, cookieHeader(DefaultCookieName+"=abc"))

	var gotSubject string
	handler := ServerStream(WithStore(store))(func(_ any, stream middleware.ServerStream) error {
		gotSubject = auth.Subject(stream.Context())
		return nil
	})

	if err := handler(nil, &testStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}
	if gotSubject != "user-1" {
		t.Errorf("auth.Subject() = %q, want %q", gotSubject, "user-1")
	}
}

func TestServerStreamRejects(t *testing.T) {
	called := false
	handler := ServerStream(WithStore(newMemStore()))(func(any, middleware.ServerStream) error {
		called = true
		return nil
	})

	err := handler(nil, &testStream{ctx: serverCtx(t, headerCarrier{})})
	if !errors.Is(err, ErrMissingSessionID) {
		t.Errorf("error = %v, want %v", err, ErrMissingSessionID)
	}
	if called {
		t.Error("the stream handler ran despite the request being rejected")
	}
}

func TestServerStreamPreservesStreamCapabilities(t *testing.T) {
	store := newMemStore(&Session{ID: "abc", Subject: "user-1"})
	underlying := &testStream{ctx: serverCtx(t, cookieHeader(DefaultCookieName+"=abc"))}

	handler := ServerStream(WithStore(store))(func(_ any, stream middleware.ServerStream) error {
		if err := stream.SendMsg("out"); err != nil {
			return err
		}
		return stream.RecvMsg(new(string))
	})
	if err := handler(nil, underlying); err != nil {
		t.Fatal(err)
	}

	if underlying.sent != 1 || underlying.received != 1 {
		t.Errorf("sent = %d, received = %d, want 1 and 1", underlying.sent, underlying.received)
	}
}

func TestExtractors(t *testing.T) {
	tests := []struct {
		name    string
		extract IDExtractor
		header  transport.Header
		want    string
	}{
		{
			name:    "cookie",
			extract: FromCookie("sid"),
			header:  cookieHeader("other=1; sid=abc"),
			want:    "abc",
		},
		{
			// The fallback is what lets metadata-based transports present the
			// same credential.
			name:    "cookie falls back to header",
			extract: FromCookie("sid"),
			header:  headerCarrier{"Sid": []string{"abc"}},
			want:    "abc",
		},
		{
			name:    "cookie absent",
			extract: FromCookie("sid"),
			header:  cookieHeader("other=1"),
			want:    "",
		},
		{
			name:    "malformed cookie header",
			extract: FromCookie("sid"),
			header:  cookieHeader("=====" + "\x00"),
			want:    "",
		},
		{
			name:    "header only",
			extract: FromHeader("X-Session"),
			header:  headerCarrier{"X-Session": []string{"abc"}},
			want:    "abc",
		},
		{
			name:    "header only ignores cookie",
			extract: FromHeader("X-Session"),
			header:  cookieHeader("X-Session=abc"),
			want:    "",
		},
		{
			name:    "no cookie header",
			extract: FromCookie("sid"),
			header:  headerCarrier{},
			want:    "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.extract(test.header); got != test.want {
				t.Errorf("extract() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestWithIDExtractor(t *testing.T) {
	store := newMemStore(&Session{ID: "abc", Subject: "user-1"})
	header := headerCarrier{"X-Session": []string{"abc"}}

	handler := Server(
		WithStore(store),
		WithIDExtractor(FromHeader("X-Session")),
	)(func(ctx context.Context, _ any) (any, error) {
		return auth.Subject(ctx), nil
	})

	reply, err := handler(serverCtx(t, header), nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "user-1" {
		t.Errorf("subject = %v, want %q", reply, "user-1")
	}
}

func TestSessionExpired(t *testing.T) {
	now := time.Unix(2000, 0)
	tests := []struct {
		name    string
		session *Session
		want    bool
	}{
		{name: "nil", session: nil, want: true},
		{name: "zero expiry never expires", session: &Session{}, want: false},
		{name: "future", session: &Session{ExpiresAt: now.Add(time.Hour)}, want: false},
		{name: "past", session: &Session{ExpiresAt: now.Add(-time.Hour)}, want: true},
		{name: "exactly now", session: &Session{ExpiresAt: now}, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.session.Expired(now); got != test.want {
				t.Errorf("Expired() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPrincipalSubject(t *testing.T) {
	if got := (Principal{}).Subject(); got != "" {
		t.Errorf("Subject() = %q, want empty for a nil session", got)
	}
	if got := (Principal{Session: &Session{Subject: "user-1"}}).Subject(); got != "user-1" {
		t.Errorf("Subject() = %q, want %q", got, "user-1")
	}
}

// Both authentication middlewares publish the same abstraction, which is why
// it belongs in the framework rather than in either module.
func TestPrincipalIsInterchangeable(t *testing.T) {
	var p auth.Principal = Principal{Session: &Session{Subject: "user-1"}}
	if got := p.Subject(); got != "user-1" {
		t.Errorf("Subject() = %q, want %q", got, "user-1")
	}
}

func TestContextRoundTrip(t *testing.T) {
	s := &Session{ID: "abc"}
	if got, ok := FromContext(NewContext(t.Context(), s)); !ok || got != s {
		t.Errorf("FromContext() = %v, %v, want the stored session", got, ok)
	}
	if _, ok := FromContext(t.Context()); ok {
		t.Error("FromContext() ok = true, want false without a session")
	}
}

func TestStoreDeleteIsIdempotent(t *testing.T) {
	store := newMemStore(&Session{ID: "abc"})
	for range 2 {
		if err := store.Delete(t.Context(), "abc"); err != nil {
			t.Fatalf("Delete() error = %v, want nil; logout must be idempotent", err)
		}
	}
}

type testStream struct {
	ctx      context.Context
	sent     int
	received int
}

func (s *testStream) Context() context.Context { return s.ctx }
func (s *testStream) SendMsg(any) error        { s.sent++; return nil }
func (s *testStream) RecvMsg(any) error        { s.received++; return nil }
