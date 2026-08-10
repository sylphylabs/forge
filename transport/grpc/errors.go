package grpc

import (
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/protoadapt"

	"github.com/sylphylabs/forge/errors"
)

// CodeOf projects a Kind onto a gRPC status code.
//
// The projection lives here rather than on Kind so that an application which
// speaks only HTTP does not link the gRPC packages to classify an error.
func CodeOf(k errors.Kind) codes.Code {
	switch k {
	case errors.KindInvalidArgument:
		return codes.InvalidArgument
	case errors.KindFailedPrecondition:
		return codes.FailedPrecondition
	case errors.KindOutOfRange:
		return codes.OutOfRange
	case errors.KindUnauthenticated:
		return codes.Unauthenticated
	case errors.KindPermissionDenied:
		return codes.PermissionDenied
	case errors.KindNotFound:
		return codes.NotFound
	case errors.KindAlreadyExists:
		return codes.AlreadyExists
	case errors.KindConflict:
		return codes.Aborted
	case errors.KindResourceExhausted:
		return codes.ResourceExhausted
	case errors.KindCanceled:
		return codes.Canceled
	case errors.KindDeadlineExceeded:
		return codes.DeadlineExceeded
	case errors.KindUnavailable:
		return codes.Unavailable
	case errors.KindUnimplemented:
		return codes.Unimplemented
	case errors.KindInternal:
		return codes.Internal
	case errors.KindDataLoss:
		return codes.DataLoss
	case errors.KindUnknown:
		return codes.Unknown
	}
	return codes.Unknown
}

// KindOf recovers a Kind from a gRPC status code.
func KindOf(c codes.Code) errors.Kind {
	switch c {
	case codes.OK, codes.Unknown:
		return errors.KindUnknown
	case codes.InvalidArgument:
		return errors.KindInvalidArgument
	case codes.FailedPrecondition:
		return errors.KindFailedPrecondition
	case codes.OutOfRange:
		return errors.KindOutOfRange
	case codes.Unauthenticated:
		return errors.KindUnauthenticated
	case codes.PermissionDenied:
		return errors.KindPermissionDenied
	case codes.NotFound:
		return errors.KindNotFound
	case codes.AlreadyExists:
		return errors.KindAlreadyExists
	case codes.Aborted:
		return errors.KindConflict
	case codes.ResourceExhausted:
		return errors.KindResourceExhausted
	case codes.Canceled:
		return errors.KindCanceled
	case codes.DeadlineExceeded:
		return errors.KindDeadlineExceeded
	case codes.Unavailable:
		return errors.KindUnavailable
	case codes.Unimplemented:
		return errors.KindUnimplemented
	case codes.Internal:
		return errors.KindInternal
	case codes.DataLoss:
		return errors.KindDataLoss
	}
	return errors.KindUnknown
}

// statusError carries a Forge error to grpc-go.
//
// grpc-go recognizes an error by the exact method GRPCStatus() *status.Status.
// Wrapping at the transport boundary keeps that contract satisfied while
// leaving the errors package free of gRPC types: the error a handler returns is
// plain, and only what leaves the process is wrapped.
type statusError struct {
	err    *errors.Error
	status *status.Status
}

func (e *statusError) Error() string { return e.err.Error() }

// Unwrap exposes the original error, so errors.Is and errors.As keep working on
// a value that has been prepared for the wire.
func (e *statusError) Unwrap() error { return e.err }

// GRPCStatus satisfies the interface grpc-go type-asserts for.
func (e *statusError) GRPCStatus() *status.Status { return e.status }

// StatusFrom projects a Forge error onto a gRPC status.
//
// Identity travels as an ErrorInfo detail, trace correlation as RequestInfo,
// and aggregate failures as BadRequest. A receiver that does not know Forge
// still sees a correct status code and message.
func StatusFrom(public errors.Public) *status.Status {
	s := status.New(CodeOf(public.Kind), public.Message)
	details := make([]protoadapt.MessageV1, 0, 3) //nolint:mnd // ErrorInfo, RequestInfo, BadRequest

	info := &errdetails.ErrorInfo{
		Reason:   public.Reason,
		Domain:   public.Domain,
		Metadata: public.Metadata,
	}
	details = append(details, info)
	if trace := public.TraceID; trace != "" {
		// google.rpc.RequestInfo.request_id is specified as "an opaque string
		// that should only be interpreted by the service generating it... used
		// to identify requests in the service's logs", which is what a trace ID
		// is for here. Using the standard detail means a receiver that does not
		// know Forge still finds the correlation handle, where a proprietary
		// detail would be invisible to it.
		//
		// It travels beside ErrorInfo rather than inside its metadata so that an
		// application's own "trace_id" entry is never shadowed.
		details = append(details, &errdetails.RequestInfo{RequestId: trace})
	}

	if violations := public.Violations; len(violations) > 0 {
		br := &errdetails.BadRequest{
			FieldViolations: make([]*errdetails.BadRequest_FieldViolation, 0, len(violations)),
		}
		for _, v := range violations {
			br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
				Field:       v.Field,
				Description: v.Description,
			})
		}
		details = append(details, br)
	}

	withDetails, attachErr := s.WithDetails(details...)
	if attachErr != nil {
		// Attaching details can only fail for a status whose code is OK, which
		// an error never has. Fall back to the bare status rather than panic.
		return s
	}
	return withDetails
}

// ErrorFrom converts a gRPC error back into a Forge error, and reports whether
// err carried a gRPC status at all.
//
// A client interceptor calls this so that application code matches a remote
// failure against the same sentinel it would use locally.
func ErrorFrom(err error) (*errors.Error, bool) {
	if err == nil {
		return nil, false
	}
	gs, ok := status.FromError(err)
	if !ok {
		return nil, false
	}
	public := errors.Public{
		Kind:    KindOf(gs.Code()),
		Message: gs.Message(),
	}
	for _, detail := range gs.Details() {
		switch d := detail.(type) {
		case *errdetails.ErrorInfo:
			public.Reason = d.GetReason()
			public.Domain = d.GetDomain()
			public.Metadata = d.GetMetadata()
		case *errdetails.RequestInfo:
			public.TraceID = d.GetRequestId()
		case *errdetails.BadRequest:
			for _, v := range d.GetFieldViolations() {
				public.Violations = append(public.Violations, errors.Violation{
					Field:       v.GetField(),
					Description: v.GetDescription(),
				})
			}
		}
	}
	return errors.FromPublic(public), true
}
