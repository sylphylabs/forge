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

// validate runs the self-validation hook and every configured validator.
func validate(msg any, validators []ValidatorFunc) error {
	if v, ok := msg.(validator); ok {
		if err := v.Validate(); err != nil {
			return errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
		}
	}
	for _, v := range validators {
		if err := v(msg); err != nil {
			return errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
		}
	}
	return nil
}
