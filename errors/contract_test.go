package errors

import (
	"strings"
	"testing"
)

// Disclosure is gated on declaration: MustDefine is the act that makes an
// identity part of a contract, and PublicOf discloses only declared
// identities. Everything else projects as a bare internal failure.

func TestPublicOfWithholdsUndeclaredIdentity(t *testing.T) {
	adHoc := Of(KindNotFound).
		WithDomain("store.internal").
		WithReason("DEVICE_NOT_FOUND").
		Msg("device dev-1 missing in tenant acme").
		Meta("tenant", "acme")

	public := PublicOf(adHoc)
	if public.Kind != KindInternal {
		t.Errorf("kind = %v, want KindInternal", public.Kind)
	}
	if public.Domain != "" || public.Reason != "" {
		t.Errorf("identity reached the snapshot: %s/%s", public.Domain, public.Reason)
	}
	if public.Message != "" || len(public.Metadata) != 0 {
		t.Errorf("undeclared data reached the snapshot: %q %v", public.Message, public.Metadata)
	}
}

// An anonymous error has no identity at all; it must not disclose its message
// either, because nothing about it was ever declared public.
func TestPublicOfWithholdsAnonymousError(t *testing.T) {
	public := PublicOf(Of(KindNotFound).Msg("row for tenant acme missing"))
	if public.Kind != KindInternal {
		t.Errorf("kind = %v, want KindInternal", public.Kind)
	}
	if public.Message != "" {
		t.Errorf("message = %q, want it withheld", public.Message)
	}
}

// The trace ID is the supported cross-process correlation handle, so it is the
// one thing a withheld error still discloses.
func TestPublicOfKeepsTraceIDWhenWithholding(t *testing.T) {
	public := PublicOf(Of(KindInternal).WithTraceID("trace-1"))
	if public.TraceID != "trace-1" {
		t.Errorf("trace ID = %q, want trace-1", public.TraceID)
	}
}

// A declared identity discloses what its caller declared, including a message
// interpolated per occurrence and violations aggregated under it.
func TestPublicOfDisclosesDeclaredIdentity(t *testing.T) {
	sentinel := MustDefine(KindNotFound, "contracttest.v1", "WIDGET_NOT_FOUND")
	e := sentinel.Msgf("widget %q not found", "w1").Meta("widget", "w1")

	public := PublicOf(e)
	if public.Kind != KindNotFound {
		t.Errorf("kind = %v, want KindNotFound", public.Kind)
	}
	if public.Domain != "contracttest.v1" || public.Reason != "WIDGET_NOT_FOUND" {
		t.Errorf("identity = %s/%s, want it intact", public.Domain, public.Reason)
	}
	if !strings.Contains(public.Message, "w1") {
		t.Errorf("message = %q, want the declared one", public.Message)
	}
	if public.Metadata["widget"] != "w1" {
		t.Errorf("metadata = %v, want the declared entry", public.Metadata)
	}
}

// Matching is by identity, not by value: an error rebuilt under a declared
// pair — the validate middleware aggregating violations, for example — still
// speaks that contract.
func TestPublicOfMatchesDeclaredIdentityByPair(t *testing.T) {
	_ = MustDefine(KindInvalidArgument, "contracttest.v1", "AGGREGATE_FAILED")
	var v Violations
	v.Add("email", "malformed")
	rebuilt := FromError(v.Err(KindInvalidArgument)).
		WithDomain("contracttest.v1").
		WithReason("AGGREGATE_FAILED")

	public := PublicOf(rebuilt)
	if public.Reason != "AGGREGATE_FAILED" {
		t.Errorf("reason = %q, want the declared identity to project", public.Reason)
	}
	if len(public.Violations) != 1 {
		t.Errorf("violations = %d, want 1", len(public.Violations))
	}
}

// A remote error was disclosed by its producer; passing it on discloses
// nothing new, so the gate does not apply.
func TestPublicOfPassesRemoteErrorThrough(t *testing.T) {
	remote := FromPublic(Public{
		Kind:    KindNotFound,
		Domain:  "peer.v1",
		Reason:  "GONE",
		Message: "gone",
	})
	public := PublicOf(remote)
	if public.Kind != KindNotFound || public.Reason != "GONE" || public.Message != "gone" {
		t.Errorf("remote error was withheld: %+v", public)
	}
}
