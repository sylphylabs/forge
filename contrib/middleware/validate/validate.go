package validate

import (
	"context"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/proto"
)

type validator interface {
	Validate() error
}

// ProtoValidate is a middleware that validates the request message with [protovalidate](https://github.com/bufbuild/protovalidate)
func ProtoValidate() middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if msg, ok := req.(proto.Message); ok {
				if err := protovalidate.Validate(msg); err != nil {
					return nil, errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
				}
			}

			// Preserve compatibility with legacy PGV-generated code and manual validators.
			if v, ok := req.(validator); ok {
				if err := v.Validate(); err != nil {
					return nil, errors.BadRequest("VALIDATOR", err.Error()).WithCause(err)
				}
			}

			return handler(ctx, req)
		}
	}
}
