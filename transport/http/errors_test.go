package http

import (
	"net/http"
	"testing"

	"github.com/sylphylabs/forge/errors"
)

// 422, 412 and 499 were the codes that collapsed to 500 under the previous
// design, when a Kind round-tripped through a foreign code space.
func TestPreviouslyLossyStatusCodes(t *testing.T) {
	tests := []struct {
		kind errors.Kind
		want int
	}{
		{errors.KindOutOfRange, http.StatusUnprocessableEntity},
		{errors.KindFailedPrecondition, http.StatusPreconditionFailed},
		{errors.KindCanceled, StatusClientClosed},
	}
	for _, tt := range tests {
		if got := StatusOf(tt.kind); got != tt.want {
			t.Errorf("StatusOf(%v) = %d, want %d", tt.kind, got, tt.want)
		}
		if got := KindOf(tt.want); got != tt.kind {
			t.Errorf("KindOf(%d) = %v, want %v", tt.want, got, tt.kind)
		}
	}
}

func TestStatusOfCoversEveryKind(t *testing.T) {
	for k := errors.KindUnknown; k <= errors.KindDataLoss; k++ {
		if got := StatusOf(k); got < 200 || got > 599 {
			t.Errorf("StatusOf(%v) = %d, outside the valid range", k, got)
		}
	}
}

func TestErrorFromStatus(t *testing.T) {
	tests := []struct {
		status int
		want   errors.Kind
	}{
		{http.StatusNotFound, errors.KindNotFound},
		{http.StatusUnprocessableEntity, errors.KindOutOfRange},
		{http.StatusServiceUnavailable, errors.KindUnavailable},
		{StatusClientClosed, errors.KindCanceled},
		{599, errors.KindInternal},
	}
	for _, tt := range tests {
		got := ErrorFromStatus(tt.status)
		if got.Kind() != tt.want {
			t.Errorf("ErrorFromStatus(%d) = %v, want %v", tt.status, got.Kind(), tt.want)
		}
		if !got.IsRemote() {
			t.Errorf("ErrorFromStatus(%d) must be marked remote", tt.status)
		}
	}
}

// A bare status with no body is what a proxy or load balancer sends. The JSON
// codec treats empty input as a no-op rather than a failure, so decoding must
// reject it explicitly: otherwise the status line is masked behind a
// zero-valued error, and a retryable 503 stops being recognized as retryable.
func TestDecodeErrorFallsBackForBodylessStatus(t *testing.T) {
	for _, body := range []string{"", "   ", "\n"} {
		decoded, ok := unmarshalProblem(ProblemContentType, []byte(body), http.StatusInternalServerError)
		if ok {
			t.Errorf("body %q decoded as a Forge error: %v", body, decoded)
		}
	}
}

// A body that names KindUnknown but carries identity is a Forge error whose
// kind the peer chose not to disclose. It must not be mistaken for foreign JSON.
func TestDecodeErrorKeepsDisclosedUnknown(t *testing.T) {
	body := []byte(`{"kind":"UNKNOWN","domain":"test.v1","reason":"OPAQUE"}`)
	decoded, ok := unmarshalProblem(ProblemContentType, body, http.StatusInternalServerError)
	if !ok {
		t.Fatal("a Forge error naming UNKNOWN was rejected")
	}
	if decoded.Reason() != "OPAQUE" {
		t.Errorf("reason = %q, want OPAQUE", decoded.Reason())
	}
}

// Foreign JSON is not a Forge error, so the status line remains the signal.
func TestDecodeErrorRejectsForeignJSON(t *testing.T) {
	if decoded, ok := unmarshalProblem(ProblemContentType, []byte(`{"unrelated":"payload"}`), http.StatusInternalServerError); ok {
		t.Errorf("foreign JSON decoded as a Forge error: %v", decoded)
	}
}
