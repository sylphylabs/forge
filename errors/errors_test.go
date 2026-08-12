package errors

import (
	stderrors "errors"
	"fmt"
	"go/build"
	"strings"
	"testing"
)

// errSentinel stands in for a service's generated error.
var errSentinel = MustDefine(KindNotFound, "test.v1", "SESSION_NOT_FOUND")

type causeError struct{ msg string }

func (e *causeError) Error() string { return e.msg }

// A typed-nil *Error reaches KindOf through errors.As, which matches it. Every
// accessor must tolerate that rather than dereference nil.
func TestTypedNilNeverPanics(t *testing.T) {
	var typed *Error
	var err error = typed

	// Each of these would panic if an accessor dereferenced without a check.
	if got := KindOf(err); got != KindUnknown {
		t.Errorf("KindOf(typed-nil) = %v, want KindUnknown", got)
	}
	if got := ReasonOf(err); got != "" {
		t.Errorf("ReasonOf(typed-nil) = %q, want empty", got)
	}
	if got := DomainOf(err); got != "" {
		t.Errorf("DomainOf(typed-nil) = %q, want empty", got)
	}
	if got := FromError(err); got == nil {
		t.Error("FromError(typed-nil) = nil, want a non-nil *Error")
	}
	if got := typed.Error(); got != "<nil>" {
		t.Errorf("(*Error)(nil).Error() = %q", got)
	}
}

func TestNilErrorHandling(t *testing.T) {
	if got := KindOf(nil); got != KindUnknown {
		t.Errorf("KindOf(nil) = %v, want KindUnknown", got)
	}
	if got := FromError(nil); got != nil {
		t.Errorf("FromError(nil) = %v, want nil", got)
	}
}

// The deriving methods share the accessors' contract: a typed-nil receiver is
// the zero-value error, so deriving from one yields a usable KindUnknown error
// rather than a panic.
func TestDerivingFromTypedNil(t *testing.T) {
	var typed *Error

	tests := []struct {
		name    string
		derived *Error
	}{
		{"Msg", typed.Msg("boom")},
		{"Msgf", typed.Msgf("boom %d", 1)},
		{"Wrap", typed.Wrap(&causeError{msg: "cause"})},
		{"Meta", typed.Meta("k", "v")},
		{"WithMetadata", typed.WithMetadata(map[string]string{"k": "v"})},
		{"WithTraceID", typed.WithTraceID("a1b2c3")},
		{"WithReason", typed.WithReason("GONE")},
		{"WithDomain", typed.WithDomain("test.v1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.derived == nil {
				t.Fatal("deriving from a typed-nil returned nil")
			}
			if got := tt.derived.Kind(); got != KindUnknown {
				t.Errorf("kind = %v, want KindUnknown", got)
			}
		})
	}

	if got := typed.Msg("boom").Message(); got != "boom" {
		t.Errorf("message = %q, want it set on the derived copy", got)
	}
	if typed.Wrap(nil) != typed {
		t.Error("Wrap(nil) on a typed-nil is not a no-op")
	}
}

// A sentinel is shared package state. Deriving from one must never change it.
func TestSentinelIsImmutable(t *testing.T) {
	base := MustDefine(KindNotFound, "test.v1", "THING_NOT_FOUND")
	derived := base.Msg("first").Meta("k", "v").Wrap(&causeError{msg: "boom"})

	if base.Message() != "" {
		t.Errorf("sentinel message = %q, want empty", base.Message())
	}
	if base.Metadata() != nil {
		t.Errorf("sentinel metadata = %v, want nil", base.Metadata())
	}
	if base.Unwrap() != nil {
		t.Errorf("sentinel cause = %v, want nil", base.Unwrap())
	}
	if derived.Message() != "first" {
		t.Errorf("derived message = %q, want %q", derived.Message(), "first")
	}

	// Two derivations from one sentinel must not observe each other.
	a := base.Msg("a")
	b := base.Msg("b")
	if a.Message() == b.Message() {
		t.Error("derived errors share message state")
	}
}

// Metadata returned to a caller must not alias the error's own map.
func TestMetadataIsCopied(t *testing.T) {
	e := errSentinel.Meta("tenant", "acme")
	md := e.Metadata()
	md["tenant"] = "mutated"
	if got := e.Metadata()["tenant"]; got != "acme" {
		t.Errorf("metadata was mutated through the returned map: got %q", got)
	}
}

func TestIsMatchesOnIdentity(t *testing.T) {
	// The message is descriptive, so two reports of one failure must match.
	a := errSentinel.Msgf("session %q", "x")
	b := errSentinel.Msgf("session %q", "y")
	if !stderrors.Is(a, errSentinel) {
		t.Error("derived error does not match its sentinel")
	}
	if !stderrors.Is(a, b) {
		t.Error("two derivations of one sentinel do not match")
	}

	// A different reason in the same domain must not match.
	other := MustDefine(KindNotFound, "test.v1", "USER_NOT_FOUND")
	if stderrors.Is(a, other) {
		t.Error("errors with different reasons matched")
	}

	// The same reason in a different domain must not match: that is what
	// domains are for.
	foreign := MustDefine(KindNotFound, "other.v1", "SESSION_NOT_FOUND")
	if stderrors.Is(a, foreign) {
		t.Error("errors from different domains matched")
	}

	// An unrelated sentinel must never match.
	if stderrors.Is(a, stderrors.New("unrelated")) {
		t.Error("matched an unrelated error")
	}
}

// An Is method must compare target shallowly. Traversing the target's chain
// reverses the standard errors.Is search direction and creates false matches.
func TestIsDoesNotUnwrapTarget(t *testing.T) {
	wrappedTarget := fmt.Errorf("target context: %w", errSentinel)
	if stderrors.Is(errSentinel, wrappedTarget) {
		t.Error("Error.Is traversed the target error chain")
	}
}

// Identity is the complete domain and reason pair. An error missing either
// half has none, so it matches only itself; two anonymous errors of one Kind
// are two unrelated failures, and matching them would make every internal
// error in a process compare equal. Classifying by Kind is KindOf's job.
func TestIsRequiresACompleteIdentity(t *testing.T) {
	tests := []struct {
		name string
		a, b *Error
		want bool
	}{
		{
			name: "same kind no reason",
			a:    Of(KindInternal),
			b:    Of(KindInternal),
			want: false,
		},
		{
			name: "same reason different kind",
			a:    MustDefine(KindNotFound, "test.v1", "GONE"),
			b:    MustDefine(KindUnavailable, "test.v1", "GONE"),
			want: true,
		},
		{
			name: "same reason no domain",
			a:    Of(KindInternal).WithReason("CACHE_CORRUPT"),
			b:    Of(KindInternal).WithReason("CACHE_CORRUPT"),
			want: false,
		},
		{
			name: "same domain no reason",
			a:    Of(KindInternal).WithDomain("test.v1"),
			b:    Of(KindInternal).WithDomain("test.v1"),
			want: false,
		},
		{
			name: "complete identity with domain and reason",
			a:    Of(KindInternal).WithDomain("test.v1").WithReason("CACHE_CORRUPT"),
			b:    Of(KindUnknown).WithDomain("test.v1").WithReason("CACHE_CORRUPT"),
			want: true,
		},
		{
			name: "sentinel derivation",
			a:    errSentinel.Msg("x"),
			b:    errSentinel,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stderrors.Is(tt.a, tt.b); got != tt.want {
				t.Errorf("Is(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// The standard library compares values before calling Is, so an anonymous
// error still matches itself even though it has no cross-instance identity.
func TestIsMatchesAnonymousErrorBySameValue(t *testing.T) {
	anon := Of(KindInternal)
	if !stderrors.Is(fmt.Errorf("ctx: %w", anon), anon) {
		t.Error("an anonymous error does not match its own value")
	}
}

// Two reports of the same identity match regardless of which Kind either
// carries: a transport boundary may reclassify the Kind in transit, and that
// must not break sentinel matching.
func TestIsIgnoresKind(t *testing.T) {
	reclassified := FromPublic(Public{
		Kind:   KindUnavailable, // a proxy rewrote the status line
		Domain: errSentinel.Domain(),
		Reason: errSentinel.Reason(),
	})
	if !stderrors.Is(reclassified, errSentinel) {
		t.Error("a reclassified error no longer matches its sentinel")
	}
}

// Matching must survive an intervening fmt.Errorf wrap.
func TestIsThroughWrapping(t *testing.T) {
	err := fmt.Errorf("handler: %w", errSentinel.Msg("gone"))
	if !stderrors.Is(err, errSentinel) {
		t.Error("wrapped error does not match its sentinel")
	}
	if got := KindOf(err); got != KindNotFound {
		t.Errorf("KindOf(wrapped) = %v, want KindNotFound", got)
	}
}

// Wrap must preserve the chain so that As reaches the underlying cause.
func TestWrapPreservesChain(t *testing.T) {
	cause := &causeError{msg: "connection refused"}
	err := errSentinel.Msg("lookup failed").Wrap(fmt.Errorf("repo: %w", cause))

	var target *causeError
	if !stderrors.As(err, &target) {
		t.Fatal("As did not reach the wrapped cause")
	}
	if target.msg != cause.msg {
		t.Errorf("reached the wrong cause: %q", target.msg)
	}
}

func TestWrapNilIsNoOp(t *testing.T) {
	cause := &causeError{msg: "connection refused"}
	err := errSentinel.Wrap(cause)
	got := err.Wrap(nil)
	if got != err {
		t.Error("Wrap(nil) returned a different error")
	}
	if !stderrors.Is(got, cause) {
		t.Error("Wrap(nil) cleared the existing cause")
	}
}

func TestEmptyViolationsYieldNoError(t *testing.T) {
	var v Violations
	if !v.Empty() {
		t.Error("a fresh Violations is not empty")
	}
	if err := v.Err(KindInvalidArgument); err != nil {
		t.Errorf("Err() on an empty set = %v, want nil", err)
	}
}

func TestKindNames(t *testing.T) {
	for k := KindUnknown; k <= KindDataLoss; k++ {
		name := k.String()
		parsed, ok := ParseKind(name)
		if !ok || parsed != k {
			t.Errorf("ParseKind(%q) = %v, %v; want %v, true", name, parsed, ok, k)
		}
	}
	if _, ok := ParseKind("NOT_A_KIND"); ok {
		t.Error("ParseKind accepted an unknown name")
	}
}

func TestErrorString(t *testing.T) {
	e := errSentinel.Msg("session is gone")
	want := "NOT_FOUND test.v1/SESSION_NOT_FOUND: session is gone"
	if got := e.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// Error follows the usual Go wrapping convention and includes its local cause.
// Transports serialize Message instead, so this detail never crosses the wire.
func TestErrorStringIncludesCause(t *testing.T) {
	e := errSentinel.Msg("outer").Wrap(&causeError{msg: "inner detail"})
	if got := e.Error(); !strings.Contains(got, "inner detail") {
		t.Errorf("Error() omitted the cause: %q", got)
	}
}

func TestErrorStringDoesNotRepeatIdenticalMessageAndCause(t *testing.T) {
	cause := &causeError{msg: "same detail"}
	e := Of(KindUnknown).Msg(cause.Error()).Wrap(cause)
	if got := e.Error(); strings.Count(got, cause.Error()) != 1 {
		t.Errorf("Error() duplicated an identical message and cause: %q", got)
	}
}

// The package must stay a leaf: a library that wants Forge's error vocabulary
// should not link protobuf reflection or the gRPC runtime to get it. Each
// transport owns its own projection for that reason, so a new import here is a
// regression rather than a detail.
func TestPackageImportsStandardLibraryOnly(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatalf("ImportDir: %v", err)
	}
	for _, path := range pkg.Imports {
		if strings.Contains(path, ".") {
			t.Errorf("non-standard import %q; move the projection to its transport", path)
		}
	}
}

// What crosses a boundary is decided by construction, not by inspection.
//
// The policy model this replaced read the Kind and guessed; it could not see a
// secret in metadata or a driver's text in a violation, and its three policies
// were writable package variables. PublicOf discloses exactly what a caller
// declared and never a cause.
func TestPublicOfExcludesTheCause(t *testing.T) {
	secret := &causeError{msg: "dial tcp 10.0.0.1:5432: password=hunter2"}
	e := errSentinel.Msg("session not found").Meta("tenant", "acme").Wrap(secret)

	public := PublicOf(e)
	if public.Message != "session not found" {
		t.Errorf("message = %q, want the declared one", public.Message)
	}
	if public.Metadata["tenant"] != "acme" {
		t.Errorf("metadata = %v, want the declared entry", public.Metadata)
	}
	if strings.Contains(public.Message, "hunter2") {
		t.Error("the cause reached the public snapshot")
	}
}

// The snapshot owns its data, so a transport cannot mutate the error it came
// from — including a shared sentinel.
func TestPublicOfOwnsItsData(t *testing.T) {
	e := errSentinel.Meta("tenant", "acme")
	public := PublicOf(e)
	public.Metadata["tenant"] = "mutated"
	if got := e.Metadata()["tenant"]; got != "acme" {
		t.Errorf("metadata was mutated through the snapshot: %q", got)
	}
}

// An error from elsewhere has text written for an operator, not a caller, so it
// discloses nothing but its classification.
func TestPublicOfForeignErrorDisclosesNothing(t *testing.T) {
	public := PublicOf(stderrors.New("dial tcp 10.0.0.1:5432: password=hunter2"))
	if public.Message != "" {
		t.Errorf("message = %q, want empty", public.Message)
	}
	if public.Kind != KindUnknown {
		t.Errorf("kind = %v, want KindUnknown", public.Kind)
	}
}

func TestPublicOfNil(t *testing.T) {
	if got := PublicOf(nil); got.Kind != KindUnknown || got.Message != "" {
		t.Errorf("PublicOf(nil) = %+v, want the zero value", got)
	}
	var typed *Error
	if got := PublicOf(typed); got.Kind != KindUnknown {
		t.Errorf("PublicOf(typed-nil) = %+v, want the zero value", got)
	}
}

// An identity is meaningful only as a complete pair; keeping half of one would
// let unrelated failures compare equal.
func TestFromPublicRequiresCompleteIdentity(t *testing.T) {
	for _, p := range []Public{
		{Kind: KindNotFound, Domain: "test.v1"},
		{Kind: KindNotFound, Reason: "GONE"},
	} {
		got := FromPublic(p)
		if got.Domain() != "" || got.Reason() != "" {
			t.Errorf("partial identity %+v was kept as %q/%q", p, got.Domain(), got.Reason())
		}
		if got.Kind() != KindNotFound {
			t.Errorf("kind = %v, want it preserved", got.Kind())
		}
	}

	complete := FromPublic(Public{Kind: KindNotFound, Domain: "test.v1", Reason: "SESSION_NOT_FOUND"})
	if !stderrors.Is(complete, errSentinel) {
		t.Error("a complete identity does not match its sentinel")
	}
	if !complete.IsRemote() {
		t.Error("a decoded error must be marked remote")
	}
	if complete.Unwrap() != nil {
		t.Error("a decoded error must carry no cause")
	}
}
