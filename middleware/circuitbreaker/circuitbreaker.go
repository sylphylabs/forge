package circuitbreaker

import (
	"context"

	"github.com/sylphylabs/forge/errors"
	internalbreaker "github.com/sylphylabs/forge/internal/circuitbreaker"
	"github.com/sylphylabs/forge/internal/group"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// ErrNotAllowed is returned when the circuit breaker is open.
var ErrNotAllowed = errors.MustDefine(errors.KindUnavailable, errors.Domain, "CIRCUIT_BREAKER_OPEN").
	Msg("request rejected because the circuit breaker is open")

// CircuitBreaker is a circuit breaker.
type CircuitBreaker = internalbreaker.CircuitBreaker

// Option is circuit breaker option.
type Option func(*options)

// WithBreakerFactory configures a factory used to lazily create one circuit breaker per operation.
func WithBreakerFactory(factory func() CircuitBreaker) Option {
	return func(o *options) {
		if factory == nil {
			return
		}
		o.group = group.NewGroup(factory)
	}
}

type options struct {
	group *group.Group[CircuitBreaker]
}

// Client circuitbreaker middleware will return errBreakerTriggered when the circuit
// breaker is triggered and the request is rejected directly.
func Client(opts ...Option) middleware.UnaryMiddleware {
	opt := &options{
		group: group.NewGroup(func() CircuitBreaker {
			return internalbreaker.NewBreaker()
		}),
	}
	for _, o := range opts {
		o(opt)
	}
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			// Outside a client transport context there is no operation to key
			// on; the empty operation shares one breaker across such calls.
			var operation string
			if info, ok := transport.FromClientContext(ctx); ok {
				operation = info.Operation()
			}
			breaker := opt.group.Get(operation)
			if err := breaker.Allow(); err != nil {
				// rejected
				// NOTE: when client reject requests locally,
				// continue to add counter let the drop ratio higher.
				breaker.MarkFailed()
				return nil, ErrNotAllowed
			}
			// allowed
			reply, err := handler(ctx, req)
			if err != nil && isServerFault(err) {
				breaker.MarkFailed()
			} else {
				breaker.MarkSuccess()
			}
			return reply, err
		}
	}
}

// isServerFault reports whether err indicates the callee is at fault, and so
// should count against the breaker.
//
// A caller-side failure — a malformed argument, a missing entity, a denied
// permission — says nothing about the callee's health and must not trip the
// breaker, however often it occurs.
func isServerFault(err error) bool {
	switch errors.KindOf(err) {
	case errors.KindInternal, errors.KindDataLoss,
		errors.KindUnavailable, errors.KindDeadlineExceeded, errors.KindUnknown:
		return true
	case errors.KindInvalidArgument, errors.KindFailedPrecondition, errors.KindOutOfRange,
		errors.KindUnauthenticated, errors.KindPermissionDenied, errors.KindNotFound,
		errors.KindAlreadyExists, errors.KindConflict, errors.KindResourceExhausted,
		errors.KindCanceled, errors.KindUnimplemented:
		return false
	}
	return false
}
