package validate

import (
	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
)

// ProtoValidateStream validates the initial request of a stream, once, before
// the handler runs.
//
// This suits server-streaming methods, where the client sends one request and
// then only receives. For client and bidirectional streaming the initial
// request is nil and nothing is validated — use [PerMessageProtoValidateStream]
// there.
func ProtoValidateStream() middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			if request != nil {
				if err := validateMessage(request); err != nil {
					return err
				}
			}
			return handler(request, stream)
		}
	}
}

// PerMessageProtoValidateStream validates every message received on a stream.
//
// This suits client and bidirectional streaming, where each message is a
// separate input worth checking. A message that fails validation fails that
// RecvMsg with a BadRequest error and leaves the stream open, so the handler
// decides whether to continue or return. The initial request, when a method
// has one, is validated too.
func PerMessageProtoValidateStream() middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			if request != nil {
				if err := validateMessage(request); err != nil {
					return err
				}
			}
			return handler(request, &validatingStream{ServerStream: stream})
		}
	}
}

// validatingStream validates every message it receives.
type validatingStream struct {
	middleware.ServerStream
}

func (s *validatingStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err
	}
	return validateMessage(m)
}

// validateMessage runs protovalidate and the legacy PGV hook.
func validateMessage(msg any) error {
	if m, ok := msg.(proto.Message); ok {
		if err := protovalidate.Validate(m); err != nil {
			return errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
		}
	}

	// Preserve compatibility with legacy PGV-generated code and manual validators.
	if v, ok := msg.(validator); ok {
		if err := v.Validate(); err != nil {
			return errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
		}
	}
	return nil
}
