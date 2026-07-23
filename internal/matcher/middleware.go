package matcher

import (
	"sort"
	"strings"

	"github.com/openkratos/kratos/middleware"
)

// Matcher is a middleware matcher.
type Matcher interface {
	Use(ms ...middleware.UnaryMiddleware)
	Add(selector string, ms ...middleware.UnaryMiddleware)
	Match(operation string) []middleware.UnaryMiddleware
}

// New new a middleware matcher.
func New() Matcher {
	return &matcher{
		matches: make(map[string][]middleware.UnaryMiddleware),
	}
}

type matcher struct {
	prefix   []string
	defaults []middleware.UnaryMiddleware
	matches  map[string][]middleware.UnaryMiddleware
}

func (m *matcher) Use(ms ...middleware.UnaryMiddleware) {
	m.defaults = ms
}

func (m *matcher) Add(selector string, ms ...middleware.UnaryMiddleware) {
	if strings.HasSuffix(selector, "*") {
		selector = strings.TrimSuffix(selector, "*")
		m.prefix = append(m.prefix, selector)
		// sort the prefix:
		//  - /foo/bar
		//  - /foo
		sort.Slice(m.prefix, func(i, j int) bool {
			return m.prefix[i] > m.prefix[j]
		})
	}
	m.matches[selector] = ms
}

func (m *matcher) Match(operation string) []middleware.UnaryMiddleware {
	ms := make([]middleware.UnaryMiddleware, 0, len(m.defaults))
	if len(m.defaults) > 0 {
		ms = append(ms, m.defaults...)
	}
	if next, ok := m.matches[operation]; ok {
		return append(ms, next...)
	}
	for _, prefix := range m.prefix {
		if strings.HasPrefix(operation, prefix) {
			return append(ms, m.matches[prefix]...)
		}
	}
	return ms
}
