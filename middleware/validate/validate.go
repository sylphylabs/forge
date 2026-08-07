package validate

import (
	"context"

	"github.com/sylphylabs/forge/middleware"
)

// ValidatorFunc defines a validation function type.
type ValidatorFunc func(v any) error

// validator is an interface for types that can validate themselves.
type validator interface {
	Validate() error
}

// Validator returns a middleware that performs validation on requests.
// It validates requests that implement Validate and any custom validators.
// Example usage:
//
// buf validate(https://github.com/bufbuild/protovalidate):
// import "buf.build/go/protovalidate"
// import "google.golang.org/protobuf/proto"
//
//	Validator(func(v any) error {
//	    if msg, ok := v.(proto.Message); ok {
//	        return protovalidate.Validate(msg)
//		}
//	    return nil
//	})
//
// Google AIP field behavior validate(https://google.aip.dev/203):
// import "go.einride.tech/aip/fieldbehavior"
// import "google.golang.org/protobuf/proto"
//
//	Validator(func(v any) error {
//	    if msg, ok := v.(proto.Message); ok {
//	        return fieldbehavior.ValidateRequiredFields(msg)
//		}
//	    return nil
//	})
func Validator(validators ...ValidatorFunc) middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if err := validate(req, validators); err != nil {
				return nil, err
			}
			return handler(ctx, req)
		}
	}
}
