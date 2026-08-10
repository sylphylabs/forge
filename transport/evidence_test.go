package transport

import (
	stderrors "errors"
	"fmt"
	"testing"
)

func TestMarkNotSentIsNilSafe(t *testing.T) {
	if got := MarkNotSent(nil); got != nil {
		t.Errorf("MarkNotSent(nil) = %v, want nil", got)
	}
	if WasNotSent(nil) {
		t.Error("WasNotSent(nil) = true, want false")
	}
}

func TestWasNotSentReportsTheMark(t *testing.T) {
	plain := stderrors.New("boom")
	if WasNotSent(plain) {
		t.Error("an unmarked error must not read as undelivered")
	}
	if !WasNotSent(MarkNotSent(plain)) {
		t.Error("a marked error must read as undelivered")
	}
}

// The mark has to survive the wrapping every layer above the transport does,
// or the evidence never reaches the retry decision.
func TestWasNotSentTravelsUpTheChain(t *testing.T) {
	base := stderrors.New("dial failed")
	wrapped := fmt.Errorf("call: %w", fmt.Errorf("attempt: %w", MarkNotSent(base)))
	if !WasNotSent(wrapped) {
		t.Error("the mark must remain visible through wrapping")
	}
	if !stderrors.Is(wrapped, base) {
		t.Error("marking must not break errors.Is on the underlying error")
	}
}

// Marking must be transparent: it records where a failure fell, and must not
// disturb how that failure identifies or classifies.
func TestMarkNotSentPreservesIdentityAndMessage(t *testing.T) {
	sentinel := stderrors.New("connection refused")
	marked := MarkNotSent(sentinel)
	if !stderrors.Is(marked, sentinel) {
		t.Error("errors.Is must still reach the marked error")
	}
	if marked.Error() != sentinel.Error() {
		t.Errorf("Error() = %q, want %q", marked.Error(), sentinel.Error())
	}
	var target *typedErr
	if !stderrors.As(MarkNotSent(&typedErr{}), &target) {
		t.Error("errors.As must still reach the marked error's type")
	}
}

type typedErr struct{}

func (*typedErr) Error() string { return "typed" }

// Marking twice must not build a deeper chain than marking once, so that a
// transport re-marking an already-marked error is a no-op.
func TestMarkNotSentIsIdempotent(t *testing.T) {
	once := MarkNotSent(stderrors.New("boom"))
	twice := MarkNotSent(once)
	if once != twice {
		t.Error("re-marking must return the error unchanged")
	}
}
