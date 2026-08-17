package generator

import (
	"strings"
	"testing"

	forgeerrors "github.com/sylphylabs/forge/errors"
)

// TestProjectDeclaredStatusMatchesRuntime locks the generator's status
// projection to the published Forge runtime: every non-zero Kind a
// declaration can resolve to must project into the 4xx/5xx range through the
// exact function the runtime error encoder uses.
func TestProjectDeclaredStatusMatchesRuntime(t *testing.T) {
	for kind := forgeerrors.KindUnknown + 1; kind <= forgeerrors.KindDataLoss; kind++ {
		status, err := projectDeclaredStatus(kind.String())
		if err != nil {
			t.Fatalf("projectDeclaredStatus(%s) error = %v", kind, err)
		}
		if status < 400 || status > 599 {
			t.Fatalf("projectDeclaredStatus(%s) = %d, outside 4xx/5xx", kind, status)
		}
	}
}

// TestProjectDeclaredStatusRejectsNonErrorStatus proves the 4xx/5xx guard.
// No real Kind projects outside the error range, so the projection is stubbed
// to produce the contradiction the guard exists for.
func TestProjectDeclaredStatusRejectsNonErrorStatus(t *testing.T) {
	original := statusOf
	statusOf = func(forgeerrors.Kind) int { return 200 }
	defer func() { statusOf = original }()

	_, err := projectDeclaredStatus(forgeerrors.KindNotFound.String())
	if err == nil || !strings.Contains(err.Error(), "not a 4xx or 5xx status") {
		t.Fatalf("projectDeclaredStatus() error = %v, want the 4xx/5xx guard", err)
	}
}

func TestProjectDeclaredStatusRejectsUnknownKindName(t *testing.T) {
	if _, err := projectDeclaredStatus("NO_SUCH_KIND"); err == nil {
		t.Fatal("projectDeclaredStatus() accepted an unknown kind name")
	}
}
