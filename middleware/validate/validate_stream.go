package validate

import (
	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

// ValidatorStream validates the initial request of a stream, once, before the
// handler runs.
//
// This suits server-streaming methods, where the client sends one request and
// then only receives. For client and bidirectional streaming the initial
// request is nil and nothing is validated — use [PerMessageValidatorStream]
// there.
func ValidatorStream(validators ...ValidatorFunc) middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			if request != nil {
				if err := validate(request, validators); err != nil {
					return err
				}
			}
			return handler(request, stream)
		}
	}
}

// PerMessageValidatorStream validates every message received on a stream.
//
// This suits client and bidirectional streaming, where each message is a
// separate input worth checking. A message that fails validation fails that
// RecvMsg with a BadRequest error and leaves the stream open, so the handler
// decides whether to continue or return. The initial request, when a method
// has one, is validated too.
func PerMessageValidatorStream(validators ...ValidatorFunc) middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			if request != nil {
				if err := validate(request, validators); err != nil {
					return err
				}
			}
			return handler(request, &validatingStream{
				ServerStream: stream,
				validators:   validators,
			})
		}
	}
}

// validatingStream validates every message it receives.
type validatingStream struct {
	middleware.ServerStream
	validators []ValidatorFunc
}

func (s *validatingStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	return validate(m, s.validators)
}

// ErrValidation identifies a request rejected by a validator.
var ErrValidation = errors.MustDefine(errors.KindInvalidArgument, errors.Domain, "VALIDATION_FAILED")

// validate runs the self-validation hook and every configured validator.
func validate(msg any, validators []ValidatorFunc) error {
	if v, ok := msg.(validator); ok {
		if err := v.Validate(); err != nil {
			return validationError(err)
		}
	}
	for _, v := range validators {
		if err := v(msg); err != nil {
			return validationError(err)
		}
	}
	return nil
}

// FieldReporter is implemented by a validation error that knows which fields
// failed. A validator whose error satisfies it produces an aggregate Forge
// error, so a client can show a user every field that was wrong rather than
// only the first.
//
// Validators that report a single opaque message need not implement it; their
// error becomes a plain [ErrValidation] with the message preserved.
type FieldReporter interface {
	// FieldViolations returns one entry per failed field.
	FieldViolations() []errors.Violation
}

// validationError converts a validator's error into a Forge error, preserving
// per-field detail when the validator reported any.
//
// The cause is always wrapped so that a handler can still reach the validator's
// own error type with [errors.As].
func validationError(err error) error {
	var reporter FieldReporter
	if errors.As(err, &reporter) {
		if fields := reporter.FieldViolations(); len(fields) > 0 {
			var v errors.Violations
			for _, f := range fields {
				v.Add(f.Field, f.Description)
			}
			return errors.FromError(v.Err(errors.KindInvalidArgument)).
				WithDomain(errors.Domain).
				WithReason("VALIDATION_FAILED").
				Msg(err.Error()).
				Wrap(err)
		}
	}
	return ErrValidation.Msg(err.Error()).Wrap(err)
}
