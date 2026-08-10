// Package protobuf projects a Forge error onto its Protobuf representation.
//
// It is separate from the errors package so that an application which never
// speaks Protobuf does not link the reflection machinery to use Forge's error
// vocabulary. Import it only where the wire format is needed.
package protobuf

import (
	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	"github.com/sylphylabs/forge/errors"
)

// protoKinds maps a Kind onto its wire enum. Both are closed vocabularies
// declared together, so the mapping is total.
var protoKinds = [...]errorapi.Kind{
	errors.KindUnknown:            errorapi.Kind_KIND_UNSPECIFIED,
	errors.KindInvalidArgument:    errorapi.Kind_KIND_INVALID_ARGUMENT,
	errors.KindFailedPrecondition: errorapi.Kind_KIND_FAILED_PRECONDITION,
	errors.KindOutOfRange:         errorapi.Kind_KIND_OUT_OF_RANGE,
	errors.KindUnauthenticated:    errorapi.Kind_KIND_UNAUTHENTICATED,
	errors.KindPermissionDenied:   errorapi.Kind_KIND_PERMISSION_DENIED,
	errors.KindNotFound:           errorapi.Kind_KIND_NOT_FOUND,
	errors.KindAlreadyExists:      errorapi.Kind_KIND_ALREADY_EXISTS,
	errors.KindConflict:           errorapi.Kind_KIND_CONFLICT,
	errors.KindResourceExhausted:  errorapi.Kind_KIND_RESOURCE_EXHAUSTED,
	errors.KindCanceled:           errorapi.Kind_KIND_CANCELED,
	errors.KindDeadlineExceeded:   errorapi.Kind_KIND_DEADLINE_EXCEEDED,
	errors.KindUnavailable:        errorapi.Kind_KIND_UNAVAILABLE,
	errors.KindUnimplemented:      errorapi.Kind_KIND_UNIMPLEMENTED,
	errors.KindInternal:           errorapi.Kind_KIND_INTERNAL,
	errors.KindDataLoss:           errorapi.Kind_KIND_DATA_LOSS,
}

// KindTo projects a Kind onto its wire enum.
func KindTo(k errors.Kind) errorapi.Kind {
	if int(k) >= len(protoKinds) {
		return errorapi.Kind_KIND_UNSPECIFIED
	}
	return protoKinds[k]
}

// KindFrom recovers a Kind from its wire enum.
func KindFrom(k errorapi.Kind) errors.Kind {
	for i, p := range protoKinds {
		if p == k {
			return errors.Kind(i) //nolint:gosec // index is bounded by protoKinds
		}
	}
	return errors.KindUnknown
}

// Marshal returns the wire representation of err.
//
// The cause chain is not included: it is local by construction. Correlate a
// failure across processes by its trace ID.
func Marshal(err *errors.Error) *errorapi.Status {
	if err == nil {
		return nil
	}
	s := &errorapi.Status{
		Kind:     KindTo(err.Kind()),
		Domain:   err.Domain(),
		Reason:   err.Reason(),
		Message:  err.Message(),
		Metadata: err.Metadata(),
		TraceId:  err.TraceID(),
	}
	if violations := err.Violations(); len(violations) > 0 {
		s.Violations = make([]*errorapi.Violation, 0, len(violations))
		for _, v := range violations {
			s.Violations = append(s.Violations, &errorapi.Violation{
				Field:       v.Field,
				Description: v.Description,
			})
		}
	}
	return s
}

// Unmarshal reconstructs an error from its wire representation.
//
// The result is marked remote: it describes a failure in another process, so it
// carries no cause and no local Go values.
func Unmarshal(s *errorapi.Status) *errors.Error {
	if s == nil {
		return nil
	}
	public := errors.Public{
		Kind:     KindFrom(s.GetKind()),
		Domain:   s.GetDomain(),
		Reason:   s.GetReason(),
		Message:  s.GetMessage(),
		Metadata: s.GetMetadata(),
		TraceID:  s.GetTraceId(),
	}
	if vs := s.GetViolations(); len(vs) > 0 {
		public.Violations = make([]errors.Violation, 0, len(vs))
		for _, v := range vs {
			public.Violations = append(public.Violations, errors.Violation{
				Field:       v.GetField(),
				Description: v.GetDescription(),
			})
		}
	}
	return errors.FromPublic(public)
}
