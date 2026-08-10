package http

import (
	"bytes"
	"encoding/json"
	"mime"

	"github.com/sylphylabs/forge/errors"
)

// ProblemContentType is the media type of an error response.
//
// See RFC 9457, "Problem Details for HTTP APIs".
const ProblemContentType = "application/problem+json"

// problem is the wire shape of an error response.
//
// An error has exactly one representation, whatever the request asked for.
// Content negotiation selects how a *result* is encoded; an error is not a
// result, and negotiating it produced two incompatible spellings of the same
// value — a client reading the shape it did not expect silently lost the kind
// or the reason, with no error raised. One shape removes the failure mode
// rather than documenting it.
//
// The field names follow the error's own vocabulary rather than RFC 9457's
// `type`/`title`/`detail`, which describe prose for a human reader. A caller
// branches on `kind` and `reason`; `trace_id` is what they quote when asking an
// operator to find the unredacted failure.
type problem struct {
	Kind       string             `json:"kind"`
	Domain     string             `json:"domain,omitempty"`
	Reason     string             `json:"reason,omitempty"`
	Message    string             `json:"message,omitempty"`
	Metadata   map[string]string  `json:"metadata,omitempty"`
	TraceID    string             `json:"trace_id,omitempty"`
	Violations []problemViolation `json:"violations,omitempty"`
}

type problemViolation struct {
	Field       string `json:"field"`
	Description string `json:"description,omitempty"`
}

// marshalProblem renders err as a problem document.
func marshalProblem(public errors.Public) ([]byte, error) {
	p := problem{
		Kind:     public.Kind.String(),
		Domain:   public.Domain,
		Reason:   public.Reason,
		Message:  public.Message,
		Metadata: public.Metadata,
		TraceID:  public.TraceID,
	}
	if violations := public.Violations; len(violations) > 0 {
		p.Violations = make([]problemViolation, 0, len(violations))
		for _, v := range violations {
			p.Violations = append(p.Violations, problemViolation{
				Field:       v.Field,
				Description: v.Description,
			})
		}
	}
	return json.Marshal(p)
}

// MaxProblemBytes bounds the error body a client will parse.
//
// A response arrives from a peer that may be broken or hostile, so the size of
// what it sends is not a number this side gets to trust. The limit is generous
// for a document that carries a kind, a reason, and a message.
const MaxProblemBytes = 64 << 10

// NoStatus is the status argument for a document that arrives without one.
//
// A stream frame is the case: its response status was sent when the stream
// opened, long before the failure happened, so there is nothing for the body to
// contradict and nothing to classify an unknown kind by.
const NoStatus = 0

// unmarshalProblem parses a problem document, and reports whether data was one.
//
// The status line is authoritative and the body only refines it. A body is
// therefore read only when it is plausibly a problem document, and it is
// rejected outright when it contradicts the status: a stale intermediary can
// serve an old body under a new status, and believing it would let a caller
// match a 503 against a NotFound sentinel and stop retrying.
//
// A body naming an unrecognized kind is still a problem document — a peer
// running a newer version may know a kind this build does not — so its identity
// is kept and only the classification falls back to the status line.
func unmarshalProblem(contentType string, data []byte, status int) (*errors.Error, bool) {
	if !isProblemContentType(contentType) {
		return nil, false
	}
	if len(data) > MaxProblemBytes {
		return nil, false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false
	}
	var p problem
	if json.Unmarshal(data, &p) != nil {
		return nil, false
	}
	if p.Kind == "" && p.Domain == "" && p.Reason == "" && p.Message == "" &&
		len(p.Metadata) == 0 && p.TraceID == "" && len(p.Violations) == 0 {
		return nil, false
	}

	kind, known := errors.ParseKind(p.Kind)
	switch {
	case known && status != NoStatus && StatusOf(kind) != status:
		// The body claims a classification the status line contradicts.
		return nil, false
	case !known && status != NoStatus:
		kind = KindOf(status)
	case !known:
		// A stream frame naming an unknown kind has no status to fall back on,
		// so the failure stays unclassified rather than being invented.
		kind = errors.KindUnknown
	}
	public := errors.Public{
		Kind:     kind,
		Domain:   p.Domain,
		Reason:   p.Reason,
		Message:  p.Message,
		Metadata: p.Metadata,
		TraceID:  p.TraceID,
	}
	if len(p.Violations) > 0 {
		public.Violations = make([]errors.Violation, 0, len(p.Violations))
		for _, v := range p.Violations {
			public.Violations = append(public.Violations, errors.Violation{
				Field:       v.Field,
				Description: v.Description,
			})
		}
	}
	return errors.FromPublic(public), true
}

// isProblemContentType reports whether a response body should be read as a
// problem document.
//
// An error response from a Forge server is always application/problem+json.
// Anything else — an nginx error page, a WAF block, a gateway's own JSON — is
// not this contract, and parsing it would let unrelated content masquerade as
// a Forge error.
func isProblemContentType(contentType string) bool {
	if contentType == "" {
		return false
	}
	if media, _, err := mime.ParseMediaType(contentType); err == nil {
		return media == ProblemContentType
	}
	return false
}
