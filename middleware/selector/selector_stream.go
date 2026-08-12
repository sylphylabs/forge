package selector

import (
	"context"

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
	prefix []string
	regex  []string
	path   []string
	match  MatchFunc

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
	b.prefix = prefix
	return b
}

// Regex is with StreamBuilder's regex.
func (b *StreamBuilder) Regex(regex ...string) *StreamBuilder {
	b.regex = regex
	return b
}

// Path is with StreamBuilder's path.
func (b *StreamBuilder) Path(path ...string) *StreamBuilder {
	b.path = path
	return b
}

// Match is with StreamBuilder's match.
func (b *StreamBuilder) Match(fn MatchFunc) *StreamBuilder {
	b.match = fn
	return b
}

// Build creates stream middleware that selects by operation. It returns an
// error if any configured regex does not compile.
func (b *StreamBuilder) Build() (middleware.StreamMiddleware, error) {
	m, err := newMatcher(b.prefix, b.regex, b.path, b.match)
	if err != nil {
		return nil, err
	}
	return streamSelector(b.transporter, m.matches, b.ms...), nil
}

// streamSelector applies ms only when the operation matches. The selected
// chain is composed once, when the middleware wraps its handler, never per
// request.
func streamSelector(transporter transporter, match func(context.Context, transporter) bool, ms ...middleware.StreamMiddleware) middleware.StreamMiddleware {
	return func(handler middleware.StreamHandler) middleware.StreamHandler {
		selected := middleware.ChainStream(ms...)(handler)
		return func(request any, stream middleware.ServerStream) error {
			if !match(stream.Context(), transporter) {
				return handler(request, stream)
			}
			return selected(request, stream)
		}
	}
}
