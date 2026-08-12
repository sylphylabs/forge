package selector

import (
	"context"
	"fmt"
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
	prefix []string
	regex  []string
	path   []string
	match  MatchFunc

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

// Build creates client middleware that selects by operation. It returns an
// error if any configured regex does not compile.
func (b *Builder) Build() (middleware.UnaryMiddleware, error) {
	m, err := newMatcher(b.prefix, b.regex, b.path, b.match)
	if err != nil {
		return nil, err
	}
	return selector(clientTransporter, m.matches, b.ms...), nil
}

// matcher evaluates compiled matching rules against the operation of the
// transport in context.
type matcher struct {
	prefix   []string
	compiled []*regexp.Regexp
	path     []string
	match    MatchFunc
}

// newMatcher compiles the matching rules, rejecting an invalid regex.
func newMatcher(prefix, regex, path []string, match MatchFunc) (*matcher, error) {
	compiled := make([]*regexp.Regexp, 0, len(regex))
	for _, expr := range regex {
		r, err := regexp.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("selector: invalid regex %q: %w", expr, err)
		}
		compiled = append(compiled, r)
	}
	return &matcher{prefix: prefix, compiled: compiled, path: path, match: match}, nil
}

// matches reports whether the operation in ctx satisfies any rule.
func (m *matcher) matches(ctx context.Context, transporter transporter) bool {
	info, ok := transporter(ctx)
	if !ok {
		return false
	}

	operation := info.Operation()
	for _, prefix := range m.prefix {
		if prefixMatch(prefix, operation) {
			return true
		}
	}
	for _, r := range m.compiled {
		if r.FindString(operation) == operation {
			return true
		}
	}
	for _, path := range m.path {
		if pathMatch(path, operation) {
			return true
		}
	}

	if m.match != nil {
		if m.match(ctx, operation) {
			return true
		}
	}

	return false
}

// selector middleware. The selected chain is composed once, when the
// middleware wraps its handler, never per request.
func selector(transporter transporter, match func(context.Context, transporter) bool, ms ...middleware.UnaryMiddleware) middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		selected := middleware.ChainUnary(ms...)(handler)
		return func(ctx context.Context, req any) (reply any, err error) {
			if !match(ctx, transporter) {
				return handler(ctx, req)
			}
			return selected(ctx, req)
		}
	}
}

func pathMatch(path string, operation string) bool {
	return path == operation
}

func prefixMatch(prefix string, operation string) bool {
	return strings.HasPrefix(operation, prefix)
}
