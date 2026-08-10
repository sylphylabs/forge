package polaris

import (
	"context"
	"strings"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/ratelimit"
	"github.com/sylphylabs/forge/transport"
	"github.com/sylphylabs/forge/transport/http"

	"github.com/polarismesh/polaris-go/pkg/model"
)

// ErrLimitExceed is service unavailable due to rate limit exceeded.
var (
	ErrLimitExceed = errors.MustDefine(errors.KindResourceExhausted, "polaris.forge.sylphylabs.io", "RATE_LIMIT_EXCEEDED").
		Msg("request rejected because the rate limit was exceeded")
)

// Ratelimit Request rate limit middleware
func Ratelimit(l Limiter) middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				var args []model.Argument
				headers := tr.RequestHeader()
				// handle header
				for _, header := range headers.Keys() {
					args = append(args, model.BuildHeaderArgument(header, headers.Get(header)))
				}
				// handle http
				if ht, ok := tr.(*http.Transport); ok {
					// url query
					for key, values := range ht.Request().URL.Query() {
						args = append(args, model.BuildQueryArgument(key, strings.Join(values, ",")))
					}
				}
				done, e := l.Allow(tr.Operation(), args...)
				if e != nil {
					// rejected
					return nil, ErrLimitExceed
				}
				// allowed
				reply, err = handler(ctx, req)
				done(ratelimit.DoneInfo{Err: err})
				return
			}
			return reply, nil
		}
	}
}
