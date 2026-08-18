package errors

import (
	"strings"
	"testing"
)

// Undisclose is the strict-mode verdict of the throws assertion: the error
// keeps its in-process identity and diagnostics, while the disclosure gate
// projects it as an internal failure.

var errUndiscloseSentinel = MustDefine(KindNotFound, "undisclose.test.v1", "FAILURE_REASON_NOT_FOUND")

func TestUndiscloseWithholdsDeclaredIdentity(t *testing.T) {
	err := errUndiscloseSentinel.Msg("book not found").Meta("id", "42").WithTraceID("trace-1")

	// Declared identity: fully disclosed before the verdict.
	if got := PublicOf(err); got.Reason != "FAILURE_REASON_NOT_FOUND" {
		t.Fatalf("PublicOf before Undisclose = %+v, want full disclosure", got)
	}

	sentenced := Undisclose(err)
	got := PublicOf(sentenced)
	want := Public{Kind: KindInternal, TraceID: "trace-1"}
	if got.Kind != want.Kind || got.TraceID != want.TraceID ||
		got.Domain != "" || got.Reason != "" || got.Message != "" || got.Metadata != nil || got.Violations != nil {
		t.Fatalf("PublicOf(Undisclose(err)) = %+v, want %+v", got, want)
	}
}

func TestUndiscloseKeepsLocalIdentityObservable(t *testing.T) {
	err := errUndiscloseSentinel.Msg("book not found")
	sentenced := Undisclose(err)

	if !Is(sentenced, errUndiscloseSentinel) {
		t.Fatal("Undisclose broke sentinel matching; logging must observe the original identity")
	}
	if KindOf(sentenced) != KindNotFound {
		t.Fatalf("KindOf = %v, want KindNotFound", KindOf(sentenced))
	}
	if !IsUndisclosed(sentenced) {
		t.Fatal("IsUndisclosed = false after Undisclose")
	}
	if IsUndisclosed(err) {
		t.Fatal("Undisclose mutated its argument")
	}
	if !strings.Contains(sentenced.Error(), "book not found") {
		t.Fatalf("Error() = %q lost the local diagnostics", sentenced.Error())
	}
}

func TestUndiscloseKeepsWrappedChainReachable(t *testing.T) {
	cause := Of(KindUnavailable).Msg("backend down")
	err := errUndiscloseSentinel.Wrap(cause)
	sentenced := Undisclose(err)

	if !Is(sentenced, errUndiscloseSentinel) {
		t.Fatal("sentinel unreachable through Undisclose")
	}
	var forge *Error
	if !As(sentenced, &forge) {
		t.Fatal("As failed through Undisclose")
	}
}

func TestUndiscloseOverridesRemotePassThrough(t *testing.T) {
	remote := FromPublic(Public{Kind: KindNotFound, Domain: "peer.v1", Reason: "PEER_REASON", Message: "peer said so", TraceID: "trace-2"})
	got := PublicOf(Undisclose(remote))
	if got.Kind != KindInternal || got.Reason != "" || got.Message != "" {
		t.Fatalf("PublicOf(Undisclose(remote)) = %+v, want internal projection", got)
	}
	if got.TraceID != "trace-2" {
		t.Fatalf("TraceID = %q, want the remote trace kept", got.TraceID)
	}
}

func TestUndiscloseNil(t *testing.T) {
	if Undisclose(nil) != nil {
		t.Fatal("Undisclose(nil) != nil")
	}
	if IsUndisclosed(nil) {
		t.Fatal("IsUndisclosed(nil) = true")
	}
}

func TestIsContractReflectsRegistry(t *testing.T) {
	if !IsContract("undisclose.test.v1", "FAILURE_REASON_NOT_FOUND") {
		t.Fatal("IsContract = false for a MustDefine identity")
	}
	if IsContract("undisclose.test.v1", "NEVER_DECLARED") {
		t.Fatal("IsContract = true for an undeclared identity")
	}
	if IsContract("", "FAILURE_REASON_NOT_FOUND") || IsContract("undisclose.test.v1", "") {
		t.Fatal("IsContract accepted a partial identity")
	}
}
