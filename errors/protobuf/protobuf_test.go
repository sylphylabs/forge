package protobuf

import (
	"testing"

	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	"github.com/sylphylabs/forge/errors"
)

func TestRoundTrip(t *testing.T) {
	var v errors.Violations
	v.Add("email", "malformed")
	original := errors.FromError(v.Err(errors.KindInvalidArgument)).
		WithDomain("test.v1").
		WithReason("VALIDATION_FAILED").
		Msg("bad request").
		Meta("tenant", "acme").
		WithTraceID("trace-1")

	restored := Unmarshal(Marshal(original))
	if restored.Kind() != original.Kind() ||
		restored.Reason() != original.Reason() ||
		restored.Domain() != original.Domain() ||
		restored.Message() != original.Message() ||
		restored.TraceID() != original.TraceID() {
		t.Errorf("round trip lost data:\n got %+v\nwant %+v", restored, original)
	}
	if len(restored.Violations()) != 1 {
		t.Errorf("violations = %d, want 1", len(restored.Violations()))
	}
	if !restored.IsRemote() {
		t.Error("a decoded error must be marked remote")
	}
}

func TestKindProjectionIsLossless(t *testing.T) {
	for k := errors.KindUnknown; k <= errors.KindDataLoss; k++ {
		if got := KindFrom(KindTo(k)); got != k {
			t.Errorf("round trip: %v -> %v -> %v", k, KindTo(k), got)
		}
	}
}

func TestNilHandling(t *testing.T) {
	if Marshal(nil) != nil {
		t.Error("Marshal(nil) must be nil")
	}
	if Unmarshal(nil) != nil {
		t.Error("Unmarshal(nil) must be nil")
	}
	if got := Unmarshal(&errorapi.Status{}); got.Kind() != errors.KindUnknown {
		t.Errorf("empty status kind = %v, want KindUnknown", got.Kind())
	}
}
