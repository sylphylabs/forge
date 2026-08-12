package http

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sylphylabs/forge/errors"
)

var problemSentinel = errors.MustDefine(errors.KindNotFound, "test.v1", "GONE")

// An error has one representation whatever the request asked for.
//
// While errors took part in content negotiation, the same value was spelled two
// ways — "NOT_FOUND" as JSON and "KIND_NOT_FOUND" as ProtoJSON. A client that
// read the shape it did not expect silently lost the kind or the reason, and
// nothing reported it. Negotiating a result is useful; negotiating a failure
// only creates ways for two peers to disagree.
func TestErrorResponseIgnoresContentNegotiation(t *testing.T) {
	accepts := []string{"", "application/json", "application/protojson", "application/xml", "*/*"}

	var first string
	for _, accept := range accepts {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		w := httptest.NewRecorder()
		DefaultErrorEncoder(w, req, problemSentinel.Msg("gone"))

		if got := w.Header().Get("Content-Type"); got != ProblemContentType {
			t.Errorf("Accept %q: content type = %q, want %q", accept, got, ProblemContentType)
		}
		if first == "" {
			first = w.Body.String()
			continue
		}
		if got := w.Body.String(); got != first {
			t.Errorf("Accept %q produced a different body:\n got %s\nwant %s", accept, got, first)
		}
	}
}

// Every server/client pairing must agree, including mismatched ones — a gateway
// may rewrite a content type, and a client may hard-code its Accept header.
func TestErrorSurvivesEveryContentTypePairing(t *testing.T) {
	sent := problemSentinel.Msg("gone")

	for _, serverAccept := range []string{"application/json", "application/protojson"} {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Accept", serverAccept)
		w := httptest.NewRecorder()
		DefaultErrorEncoder(w, req, sent)

		// An error body is always the problem media type; a peer sending
		// something else is covered by TestNonProblemMediaTypeIsNotParsed.
		for _, clientCT := range []string{ProblemContentType, ProblemContentType + "; charset=utf-8"} {
			res := &http.Response{
				Header:     http.Header{"Content-Type": []string{clientCT}},
				StatusCode: w.Code,
				Body:       io.NopCloser(bytes.NewReader(w.Body.Bytes())),
			}
			got := DefaultErrorDecoder(context.Background(), res)
			if errors.KindOf(got) != errors.KindNotFound {
				t.Errorf("server %q client %q: kind = %v, want KindNotFound",
					serverAccept, clientCT, errors.KindOf(got))
			}
			if errors.ReasonOf(got) != "GONE" {
				t.Errorf("server %q client %q: reason = %q, want GONE",
					serverAccept, clientCT, errors.ReasonOf(got))
			}
			if !errors.Is(got, problemSentinel) {
				t.Errorf("server %q client %q: does not match its sentinel", serverAccept, clientCT)
			}
		}
	}
}

// A kind this build does not recognize still names a real failure. Its identity
// is kept and only the classification falls back to the status line, so a peer
// running a newer version stays understandable.
func TestUnknownKindKeepsIdentity(t *testing.T) {
	body := []byte(`{"kind":"KIND_FROM_THE_FUTURE","domain":"test.v1","reason":"GONE"}`)
	got, ok := unmarshalProblem(ProblemContentType, body, http.StatusNotFound)
	if !ok {
		t.Fatal("a document naming an unknown kind was rejected")
	}
	if got.Kind() != errors.KindNotFound {
		t.Errorf("kind = %v, want it classified by the 404 status", got.Kind())
	}
	if got.Reason() != "GONE" {
		t.Errorf("reason = %q, want GONE", got.Reason())
	}
}

// A stale intermediary can serve an old body under a new status, and a proxy
// can rewrite a status under a fresh body. The status line wins the
// classification either way — a caller keeps retrying a 503 — but the
// document's identity and diagnostics are kept: they are the only reason,
// metadata, and trace the peer sent.
func TestStatusContradictingBodyIsReclassified(t *testing.T) {
	res := &http.Response{
		Header:     http.Header{"Content-Type": []string{ProblemContentType}},
		StatusCode: http.StatusServiceUnavailable,
		Body: io.NopCloser(bytes.NewBufferString(
			`{"kind":"NOT_FOUND","domain":"test.v1","reason":"GONE","trace_id":"a1b2c3"}`)),
	}
	got := DefaultErrorDecoder(context.Background(), res)
	if errors.KindOf(got) != errors.KindUnavailable {
		t.Errorf("kind = %v, want the status line to win", errors.KindOf(got))
	}
	if errors.ReasonOf(got) != "GONE" {
		t.Errorf("reason = %q, want it kept", errors.ReasonOf(got))
	}
	if got := errors.FromError(got).TraceID(); got != "a1b2c3" {
		t.Errorf("trace_id = %q, want it kept", got)
	}
}

// A proxy that rewrites a status it does not know — 499 and 412 are the usual
// victims, flattened to 400 — must not cost the caller the document's reason,
// metadata, and trace. The status line reclassifies the failure; everything
// the peer said about it survives.
func TestProxyStatusRewriteKeepsDiagnostics(t *testing.T) {
	tests := []struct {
		name     string
		kind     errors.Kind
		rewrite  int
		wantKind errors.Kind
	}{
		{"499 to 400", errors.KindCanceled, http.StatusBadRequest, errors.KindInvalidArgument},
		{"412 to 400", errors.KindFailedPrecondition, http.StatusBadRequest, errors.KindInvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sent := errors.MustDefine(tt.kind, "test.v1", "GONE").
				Msg("gone").
				Meta("tenant", "acme").
				WithTraceID("a1b2c3")
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			w := httptest.NewRecorder()
			DefaultErrorEncoder(w, req, sent)
			if w.Code == tt.rewrite {
				t.Fatalf("status %d does not exercise a rewrite", w.Code)
			}

			// The proxy rewrote the status line; the body is untouched.
			res := &http.Response{
				Header:     http.Header{"Content-Type": []string{ProblemContentType}},
				StatusCode: tt.rewrite,
				Body:       io.NopCloser(bytes.NewReader(w.Body.Bytes())),
			}
			got := errors.FromError(DefaultErrorDecoder(context.Background(), res))
			if got.Kind() != tt.wantKind {
				t.Errorf("kind = %v, want %v from the rewritten status", got.Kind(), tt.wantKind)
			}
			if got.Reason() != "GONE" || got.Domain() != "test.v1" {
				t.Errorf("identity = %q/%q, want it kept", got.Domain(), got.Reason())
			}
			if !errors.Is(got, sent) {
				t.Error("the rewritten response no longer matches its sentinel")
			}
			if got.Metadata()["tenant"] != "acme" {
				t.Errorf("metadata = %v, want it kept", got.Metadata())
			}
			if got.TraceID() != "a1b2c3" {
				t.Errorf("trace_id = %q, want it kept", got.TraceID())
			}
		})
	}
}

// An error page from a proxy is not this contract, and parsing it would let
// unrelated content masquerade as a Forge error.
func TestNonProblemMediaTypeIsNotParsed(t *testing.T) {
	for _, ct := range []string{"text/html", "application/json", ""} {
		res := &http.Response{
			Header:     http.Header{"Content-Type": []string{ct}},
			StatusCode: http.StatusServiceUnavailable,
			Body:       io.NopCloser(bytes.NewBufferString(`{"kind":"NOT_FOUND","domain":"d","reason":"R"}`)),
		}
		got := DefaultErrorDecoder(context.Background(), res)
		if errors.KindOf(got) != errors.KindUnavailable {
			t.Errorf("content type %q: kind = %v, want the status line to win", ct, errors.KindOf(got))
		}
	}
}

// A media type with parameters is still the problem media type.
func TestProblemMediaTypeAcceptsParameters(t *testing.T) {
	res := &http.Response{
		Header:     http.Header{"Content-Type": []string{ProblemContentType + "; charset=utf-8"}},
		StatusCode: http.StatusNotFound,
		Body: io.NopCloser(bytes.NewBufferString(
			`{"kind":"NOT_FOUND","domain":"test.v1","reason":"GONE"}`)),
	}
	if got := DefaultErrorDecoder(context.Background(), res); !errors.Is(got, problemSentinel) {
		t.Errorf("a parameterized media type was rejected: %v", got)
	}
}

// The size of what a peer sends is not a number this side gets to trust.
func TestOversizedBodyIsRejected(t *testing.T) {
	huge := bytes.Repeat([]byte("a"), MaxProblemBytes+1)
	body := append([]byte(`{"kind":"NOT_FOUND","domain":"test.v1","reason":"GONE","message":"`), huge...)
	body = append(body, []byte(`"}`)...)

	res := &http.Response{
		Header:     http.Header{"Content-Type": []string{ProblemContentType}},
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
	got := DefaultErrorDecoder(context.Background(), res)
	if errors.Is(got, problemSentinel) {
		t.Error("an oversized body was accepted")
	}
	if errors.KindOf(got) != errors.KindNotFound {
		t.Errorf("kind = %v, want it classified by the status line", errors.KindOf(got))
	}
}

// A stream frame arrives without a status: the response status was sent when
// the stream opened, long before the failure. There is therefore nothing to
// reclassify by, and the body's kind is taken at its word.
func TestNoStatusAcceptsAnyKind(t *testing.T) {
	body := []byte(`{"kind":"NOT_FOUND","domain":"test.v1","reason":"GONE"}`)

	// With a status that disagrees, the status line reclassifies.
	got, ok := unmarshalProblem(ProblemContentType, body, http.StatusInternalServerError)
	if !ok {
		t.Fatal("a contradicted document was rejected")
	}
	if got.Kind() != errors.KindInternal {
		t.Errorf("kind = %v, want the status line to win", got.Kind())
	}
	// With no status, there is nothing to contradict.
	got, ok = unmarshalProblem(ProblemContentType, body, NoStatus)
	if !ok {
		t.Fatal("a stream frame was rejected for having no status")
	}
	if got.Kind() != errors.KindNotFound {
		t.Errorf("kind = %v, want the body believed", got.Kind())
	}
	if !errors.Is(got, problemSentinel) {
		t.Errorf("stream frame lost its identity: %v", got)
	}
}

// Without a status there is nothing to classify an unknown kind by, so the
// failure stays unclassified rather than being invented.
func TestNoStatusLeavesUnknownKindUnclassified(t *testing.T) {
	body := []byte(`{"kind":"KIND_FROM_THE_FUTURE","domain":"test.v1","reason":"GONE"}`)
	got, ok := unmarshalProblem(ProblemContentType, body, NoStatus)
	if !ok {
		t.Fatal("a document naming an unknown kind was rejected")
	}
	if got.Kind() != errors.KindUnknown {
		t.Errorf("kind = %v, want KindUnknown", got.Kind())
	}
	if got.Reason() != "GONE" {
		t.Errorf("reason = %q, want it kept", got.Reason())
	}
}
