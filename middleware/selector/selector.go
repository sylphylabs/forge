package selector

import (
	"context"
	"regexp"
	"strings"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

type (
	transporter func(ctx context.Context) (transport.Transporter, bool)
	MatchFunc   func(ctx context.Context, operation string) bool
)

// clientTransporter is get client transport.Transporter from ctx
var clientTransporter transporter = func(ctx context.Context) (transport.Transporter, bool) {
	return transport.FromClientContext(ctx)
}

// Builder is a selector builder
type Builder struct {
	prefix   []string
	regex    []string
	path     []string
	match    MatchFunc
	compiled []*regexp.Regexp

	ms []middleware.UnaryMiddleware
}

// Client selector middleware
func Client(ms ...middleware.UnaryMiddleware) *Builder {
	return &Builder{ms: ms}
}

// Prefix is with Builder's prefix
func (b *Builder) Prefix(prefix ...string) *Builder {
	b.prefix = prefix
	return b
}

// Regex is with Builder's regex
func (b *Builder) Regex(regex ...string) *Builder {
	b.regex = regex
	return b
}

// Path is with Builder's path
func (b *Builder) Path(path ...string) *Builder {
	b.path = path
	return b
}

// Match is with Builder's match
func (b *Builder) Match(fn MatchFunc) *Builder {
	b.match = fn
	return b
}

// Build creates client middleware that selects by operation.
func (b *Builder) Build() middleware.UnaryMiddleware {
	b.compiled = make([]*regexp.Regexp, 0, len(b.regex))
	for _, regex := range b.regex {
		if r, err := regexp.Compile(regex); err == nil {
			b.compiled = append(b.compiled, r)
		}
	}
	return selector(clientTransporter, b.matches, b.ms...)
}

// matches is match operation compliance Builder
func (b *Builder) matches(ctx context.Context, transporter transporter) bool {
	info, ok := transporter(ctx)
	if !ok {
		return false
	}

	operation := info.Operation()
	for _, prefix := range b.prefix {
		if prefixMatch(prefix, operation) {
			return true
		}
	}
	for _, r := range b.compiled {
		if r.FindString(operation) == operation {
			return true
		}
	}
	for _, path := range b.path {
		if pathMatch(path, operation) {
			return true
		}
	}

	if b.match != nil {
		if b.match(ctx, operation) {
			return true
		}
	}

	return false
}

// selector middleware
func selector(transporter transporter, match func(context.Context, transporter) bool, ms ...middleware.UnaryMiddleware) middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			if !match(ctx, transporter) {
				return handler(ctx, req)
			}
			return middleware.ChainUnary(ms...)(handler)(ctx, req)
		}
	}
}

func pathMatch(path string, operation string) bool {
	return path == operation
}

func prefixMatch(prefix string, operation string) bool {
	return strings.HasPrefix(operation, prefix)
}
