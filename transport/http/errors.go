package http

import (
	"fmt"
	"net/http"

	"github.com/sylphylabs/forge/errors"
)

// StatusClientClosed is the non-standard status code defined by nginx for a
// request whose client disconnected before a response was sent.
//
// See https://httpstatus.in/499/.
const StatusClientClosed = 499

// StatusOf projects a Kind onto an HTTP status code.
//
// The projection lives here rather than on Kind because it encodes HTTP's
// judgement, not the error's: KindAlreadyExists and KindConflict both map to
// 409 because HTTP has one code for both, which is a fact about HTTP.
func StatusOf(k errors.Kind) int {
	switch k {
	case errors.KindInvalidArgument:
		return http.StatusBadRequest
	case errors.KindFailedPrecondition:
		return http.StatusPreconditionFailed
	case errors.KindOutOfRange:
		return http.StatusUnprocessableEntity
	case errors.KindUnauthenticated:
		return http.StatusUnauthorized
	case errors.KindPermissionDenied:
		return http.StatusForbidden
	case errors.KindNotFound:
		return http.StatusNotFound
	case errors.KindAlreadyExists, errors.KindConflict:
		return http.StatusConflict
	case errors.KindResourceExhausted:
		return http.StatusTooManyRequests
	case errors.KindCanceled:
		return StatusClientClosed
	case errors.KindDeadlineExceeded:
		return http.StatusGatewayTimeout
	case errors.KindUnavailable:
		return http.StatusServiceUnavailable
	case errors.KindUnimplemented:
		return http.StatusNotImplemented
	case errors.KindInternal, errors.KindDataLoss, errors.KindUnknown:
		return http.StatusInternalServerError
	}
	return http.StatusInternalServerError
}

// KindOf recovers a Kind from an HTTP status code.
//
// It is used when a response carries no Forge error representation, so the
// status line is the only signal available.
func KindOf(status int) errors.Kind {
	switch status {
	case http.StatusBadRequest:
		return errors.KindInvalidArgument
	case http.StatusUnauthorized:
		return errors.KindUnauthenticated
	case http.StatusForbidden:
		return errors.KindPermissionDenied
	case http.StatusNotFound:
		return errors.KindNotFound
	case http.StatusConflict:
		return errors.KindConflict
	case http.StatusPreconditionFailed:
		return errors.KindFailedPrecondition
	case http.StatusUnprocessableEntity:
		return errors.KindOutOfRange
	case http.StatusTooManyRequests:
		return errors.KindResourceExhausted
	case StatusClientClosed:
		return errors.KindCanceled
	case http.StatusNotImplemented:
		return errors.KindUnimplemented
	case http.StatusServiceUnavailable:
		return errors.KindUnavailable
	case http.StatusGatewayTimeout:
		return errors.KindDeadlineExceeded
	}
	if status >= 500 {
		return errors.KindInternal
	}
	if status >= 400 {
		return errors.KindInvalidArgument
	}
	return errors.KindUnknown
}

// ErrorFromStatus returns an error classified by an HTTP status code.
//
// It is the fallback for a response that carried no Forge error
// representation. The result is marked remote and carries no reason: the peer
// did not supply one.
func ErrorFromStatus(status int) *errors.Error {
	return errors.FromPublic(errors.Public{
		Kind:    KindOf(status),
		Message: statusText(status),
	})
}

// statusText describes a status code for a human reader.
func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return text
	}
	if status == StatusClientClosed {
		return "Client Closed Request"
	}
	return fmt.Sprintf("HTTP status %d", status)
}
