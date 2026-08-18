package throws

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/sylphylabs/forge/errors"
)

// The declared contract of the fixture method, spelled the way generated
// *_errors.pb.go files spell it.
var (
	errDeclared   = errors.MustDefine(errors.KindNotFound, "throws.test.v1", "FAILURE_REASON_NOT_FOUND")
	errUndeclared = errors.MustDefine(errors.KindConflict, "throws.test.v1", "FAILURE_REASON_SURPRISE")
)

const fixtureMethod = "throws.test.v1.LibraryService/GetBook"

func newFixture(t *testing.T, opts ...Option) (*Declaration, Assert, *bytes.Buffer) {
	t.Helper()
	declaration := Declare(fixtureMethod, Identity{Domain: "throws.test.v1", Reason: "FAILURE_REASON_NOT_FOUND"})
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	opts = append([]Option{WithLogger(logger), withEnv(func(string) string { return "" })}, opts...)
	config, err := NewConfig(opts, declaration)
	if err != nil {
		t.Fatal(err)
	}
	return declaration, config.Asserter(declaration), &buf
}

func TestDeclaredIdentityPassesSilently(t *testing.T) {
	_, assert, buf := newFixture(t)
	in := errDeclared.Msg("book not found")
	if out := assert(context.Background(), in); out != in { //nolint:errorlint // pass-through must be the same value
		t.Fatalf("assert rewrote a declared error: %v", out)
	}
	if buf.Len() != 0 {
		t.Fatalf("declared identity logged a violation: %s", buf.String())
	}
}

func TestNilErrorPasses(t *testing.T) {
	_, assert, _ := newFixture(t)
	if out := assert(context.Background(), nil); out != nil {
		t.Fatalf("assert(nil) = %v", out)
	}
}

func TestFrameworkDomainIsExempt(t *testing.T) {
	_, assert, buf := newFixture(t)
	in := errors.MustDefine(errors.KindResourceExhausted, errors.Domain, "RATE_LIMITED").Msg("slow down")
	if out := assert(context.Background(), in); out != in { //nolint:errorlint // pass-through must be the same value
		t.Fatalf("assert rewrote a framework error: %v", out)
	}
	if buf.Len() != 0 {
		t.Fatalf("framework identity logged a violation: %s", buf.String())
	}
}

func TestAnonymousLocalErrorIsExempt(t *testing.T) {
	_, assert, buf := newFixture(t)
	// An Of() product projects as a bare internal error; it cannot contradict
	// the document.
	in := errors.Of(errors.KindInternal).Msg("driver exploded")
	if out := assert(context.Background(), in); out != in { //nolint:errorlint // pass-through must be the same value
		t.Fatalf("assert rewrote an anonymous local error: %v", out)
	}
	if buf.Len() != 0 {
		t.Fatalf("anonymous identity logged a violation: %s", buf.String())
	}
}

func TestObserveLogsUndeclaredContractIdentity(t *testing.T) {
	_, assert, buf := newFixture(t)
	in := errUndeclared.Msg("edition changed")
	out := assert(context.Background(), in)
	if out != in { //nolint:errorlint // observe mode passes the same value
		t.Fatalf("observe mode rewrote the error: %v", out)
	}
	logged := buf.String()
	for _, want := range []string{
		"undeclared error identity",
		fixtureMethod,
		"FAILURE_REASON_SURPRISE",
		"throws.test.v1",
		"declare FAILURE_REASON_SURPRISE in the (throws) option",
	} {
		if !strings.Contains(logged, want) {
			t.Fatalf("violation log missing %q:\n%s", want, logged)
		}
	}
}

func TestObserveLogsUndeclaredRemoteIdentity(t *testing.T) {
	_, assert, buf := newFixture(t)
	// A remote identity passes PublicOf verbatim, so an undeclared one is the
	// unstranslated-foreign-sentinel violation the assertion must catch.
	in := errors.FromPublic(errors.Public{
		Kind: errors.KindNotFound, Domain: "peer.v1", Reason: "PEER_FAILURE", Message: "peer failed",
	})
	out := assert(context.Background(), in)
	if out != error(in) { //nolint:errorlint // observe mode passes the same value
		t.Fatalf("observe mode rewrote the error: %v", out)
	}
	logged := buf.String()
	if !strings.Contains(logged, "PEER_FAILURE") || !strings.Contains(logged, "remote=true") {
		t.Fatalf("remote violation not logged as such:\n%s", logged)
	}
}

func TestStrictMarksViolationUndisclosed(t *testing.T) {
	_, assert, buf := newFixture(t, Strict())
	in := errUndeclared.Msg("edition changed").WithTraceID("trace-9")
	out := assert(context.Background(), in)

	if !errors.IsUndisclosed(out) {
		t.Fatal("strict mode did not mark the violation undisclosed")
	}
	// The in-process identity survives for logging and metrics...
	if !errors.Is(out, errUndeclared) {
		t.Fatal("strict verdict broke sentinel matching")
	}
	// ...while the disclosure gate projects an internal failure with trace.
	public := errors.PublicOf(out)
	if public.Kind != errors.KindInternal || public.Reason != "" || public.TraceID != "trace-9" {
		t.Fatalf("PublicOf(strict verdict) = %+v, want internal + trace", public)
	}
	if !strings.Contains(buf.String(), "undeclared error identity") {
		t.Fatal("strict mode skipped the violation log")
	}
}

func TestStrictDoesNotTouchDeclaredIdentity(t *testing.T) {
	_, assert, _ := newFixture(t, Strict())
	in := errDeclared.Msg("book not found")
	if out := assert(context.Background(), in); out != in { //nolint:errorlint // pass-through must be the same value
		t.Fatalf("strict mode rewrote a declared error: %v", out)
	}
}

func TestStrictByMethodName(t *testing.T) {
	_, assert, _ := newFixture(t, Strict("GetBook"))
	if out := assert(context.Background(), errUndeclared); !errors.IsUndisclosed(out) {
		t.Fatal("Strict(\"GetBook\") did not apply to the method")
	}
}

func TestStrictUnknownMethodFailsConstruction(t *testing.T) {
	declaration := Declare(fixtureMethod)
	_, err := NewConfig([]Option{Strict("NoSuchMethod")}, declaration)
	if err == nil || !strings.Contains(err.Error(), "NoSuchMethod") {
		t.Fatalf("NewConfig error = %v, want unknown method diagnosis", err)
	}
}

func TestFailUndeclaredReplacesViolation(t *testing.T) {
	_, assert, _ := newFixture(t, FailUndeclared())
	in := errUndeclared.Msg("edition changed")
	out := assert(context.Background(), in)
	if !errors.Is(out, ErrUndeclared) {
		t.Fatalf("fail mode did not verdict ErrUndeclared: %v", out)
	}
	if !errors.Is(out, errUndeclared) {
		t.Fatal("fail verdict lost the violating error")
	}
	if !strings.Contains(out.Error(), fixtureMethod) {
		t.Fatalf("fail verdict does not name the method: %v", out)
	}
}

func TestEnvironmentUpgradesToFail(t *testing.T) {
	declaration := Declare(fixtureMethod, Identity{Domain: "throws.test.v1", Reason: "FAILURE_REASON_NOT_FOUND"})
	config, err := NewConfig([]Option{
		WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))),
		withEnv(func(key string) string {
			if key != EnvVar {
				t.Fatalf("looked up %q, want %q", key, EnvVar)
			}
			return EnvFail
		}),
	}, declaration)
	if err != nil {
		t.Fatal(err)
	}
	out := config.Asserter(declaration)(context.Background(), errUndeclared)
	if !errors.Is(out, ErrUndeclared) {
		t.Fatalf("FORGE_THROWS=fail did not upgrade observe to fail: %v", out)
	}
}

func TestStrictWinsOverFail(t *testing.T) {
	_, assert, _ := newFixture(t, FailUndeclared(), Strict())
	out := assert(context.Background(), errUndeclared)
	if !errors.IsUndisclosed(out) || errors.Is(out, ErrUndeclared) {
		t.Fatalf("strict did not take precedence over fail: %v", out)
	}
}

func TestAlreadyUndisclosedPasses(t *testing.T) {
	_, assert, buf := newFixture(t)
	in := errors.Undisclose(errUndeclared.Msg("already sentenced"))
	if out := assert(context.Background(), in); out != in { //nolint:errorlint // pass-through must be the same value
		t.Fatalf("assert rewrote an already-undisclosed error: %v", out)
	}
	if buf.Len() != 0 {
		t.Fatalf("undisclosed error logged a violation: %s", buf.String())
	}
}

func TestUnobservedTracksReverseObservation(t *testing.T) {
	declaration, assert, _ := newFixture(t)
	want := []Identity{{Domain: "throws.test.v1", Reason: "FAILURE_REASON_NOT_FOUND"}}
	if got := declaration.Unobserved(); len(got) != 1 || got[0] != want[0] {
		t.Fatalf("Unobserved before any traffic = %v, want %v", got, want)
	}
	_ = assert(context.Background(), errDeclared)
	if got := declaration.Unobserved(); len(got) != 0 {
		t.Fatalf("Unobserved after the identity fired = %v, want empty", got)
	}
}

func TestDeclaredIsSortedAndOwned(t *testing.T) {
	declaration := Declare(fixtureMethod,
		Identity{Domain: "b.v1", Reason: "R"},
		Identity{Domain: "a.v1", Reason: "Z"},
		Identity{Domain: "a.v1", Reason: "A"},
	)
	got := declaration.Declared()
	want := []Identity{{Domain: "a.v1", Reason: "A"}, {Domain: "a.v1", Reason: "Z"}, {Domain: "b.v1", Reason: "R"}}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Declared() = %v, want %v", got, want)
		}
	}
	if declaration.Method() != fixtureMethod {
		t.Fatalf("Method() = %q", declaration.Method())
	}
}

// The assertion must extract the identity the transport encoders would put on
// the wire: the first *Error in the chain, exactly as errors.FromError and
// errors.PublicOf select it.
func TestIdentityExtractionMatchesDisclosure(t *testing.T) {
	_, assert, buf := newFixture(t)
	wrapped := errUndeclared.Wrap(errDeclared.Msg("inner declared"))
	_ = assert(context.Background(), wrapped)
	if !strings.Contains(buf.String(), "FAILURE_REASON_SURPRISE") {
		t.Fatalf("assert did not judge the identity PublicOf would disclose:\n%s", buf.String())
	}
}
