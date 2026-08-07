package validate

import (
	"context"

	"github.com/sylphylabs/forge/middleware"
)

type validator interface {
	Validate() error
}

// ProtoValidate is a middleware that validates the request message with [protovalidate](https://github.com/bufbuild/protovalidate)
func ProtoValidate() middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if err := validateMessage(req); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}
	}
}
