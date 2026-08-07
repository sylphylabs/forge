package selector

import (
	"context"
	"regexp"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

// serverTransporter gets the server transport.Transporter from ctx.
var serverTransporter transporter = func(ctx context.Context) (transport.Transporter, bool) {
	return transport.FromServerContext(ctx)
}

// StreamBuilder selects the stream middleware applied to an operation. The
// matching rules are the ones of [Builder]; only the composed middleware kind
// differs.
type StreamBuilder struct {
	Builder

	transporter transporter
	ms          []middleware.StreamMiddleware
}

// ServerStream selects server stream middleware by operation.
func ServerStream(ms ...middleware.StreamMiddleware) *StreamBuilder {
	return &StreamBuilder{transporter: serverTransporter, ms: ms}
}

// ClientStream selects client stream middleware by operation.
func ClientStream(ms ...middleware.StreamMiddleware) *StreamBuilder {
	return &StreamBuilder{transporter: clientTransporter, ms: ms}
}

// Prefix is with StreamBuilder's prefix.
func (b *StreamBuilder) Prefix(prefix ...string) *StreamBuilder {
	b.Builder.Prefix(prefix...)
	return b
}

// Regex is with StreamBuilder's regex.
func (b *StreamBuilder) Regex(regex ...string) *StreamBuilder {
	b.Builder.Regex(regex...)
	return b
}

// Path is with StreamBuilder's path.
func (b *StreamBuilder) Path(path ...string) *StreamBuilder {
	b.Builder.Path(path...)
	return b
}

// Match is with StreamBuilder's match.
func (b *StreamBuilder) Match(fn MatchFunc) *StreamBuilder {
	b.Builder.Match(fn)
	return b
}

// Build creates stream middleware that selects by operation.
func (b *StreamBuilder) Build() middleware.StreamMiddleware {
	b.compiled = make([]*regexp.Regexp, 0, len(b.regex))
	for _, regex := range b.regex {
		if r, err := regexp.Compile(regex); err == nil {
			b.compiled = append(b.compiled, r)
		}
	}
	return streamSelector(b.transporter, b.matches, b.ms...)
}

// streamSelector applies ms only when the operation matches.
func streamSelector(transporter transporter, match func(context.Context, transporter) bool, ms ...middleware.StreamMiddleware) middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			if !match(stream.Context(), transporter) {
				return handler(request, stream)
			}
			return middleware.ChainStream(ms...)(handler)(request, stream)
		}
	}
}
