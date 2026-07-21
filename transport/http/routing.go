package http

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
)

const internalPathValuePrefix = "__openkratos"

type routeContextKey struct{}

type matchedRoute struct {
	route *compiledRoute
}

type routePart struct {
	literal    string
	pathValue  string
	trimSuffix string
}

type routeVariable struct {
	name     string
	parts    []routePart
	validate *regexp.Regexp
}

type compiledRoute struct {
	pattern  string
	template string
	vars     []routeVariable
}

func (r *compiledRoute) match(req *http.Request) bool {
	for _, variable := range r.vars {
		value, ok := variable.value(req)
		if !ok || variable.validate != nil && !variable.validate.MatchString(value) {
			return false
		}
	}
	return true
}

func (r *compiledRoute) setPathValues(req *http.Request) {
	for _, variable := range r.vars {
		value, ok := variable.value(req)
		if ok {
			req.SetPathValue(variable.name, value)
		}
	}
}

func (r *compiledRoute) values(req *http.Request) url.Values {
	values := make(url.Values, len(r.vars))
	for _, variable := range r.vars {
		value, ok := variable.value(req)
		if ok {
			values[variable.name] = []string{value}
		}
	}
	return values
}

func (v *routeVariable) value(req *http.Request) (string, bool) {
	if len(v.parts) == 1 {
		part := v.parts[0]
		if part.pathValue == "" {
			return part.literal, true
		}
		value := req.PathValue(part.pathValue)
		if part.trimSuffix != "" {
			if !strings.HasSuffix(value, part.trimSuffix) {
				return "", false
			}
			value = strings.TrimSuffix(value, part.trimSuffix)
		}
		return value, true
	}
	parts := make([]string, 0, len(v.parts))
	for _, part := range v.parts {
		value := part.literal
		if part.pathValue != "" {
			value = req.PathValue(part.pathValue)
			if part.trimSuffix != "" {
				if !strings.HasSuffix(value, part.trimSuffix) {
					return "", false
				}
				value = strings.TrimSuffix(value, part.trimSuffix)
			}
		}
		parts = append(parts, value)
	}
	return strings.Join(parts, "/"), true
}

type routeVariant struct {
	route   compiledRoute
	handler http.Handler
}

type routeBucket struct {
	router *routeMux
	mu     sync.Mutex
	routes atomic.Pointer[[]routeVariant]
}

func (b *routeBucket) add(route routeVariant) {
	b.mu.Lock()
	current := b.routes.Load()
	routes := make([]routeVariant, 0, 1)
	if current != nil {
		routes = make([]routeVariant, 0, len(*current)+1)
		routes = append(routes, (*current)...)
	}
	routes = append(routes, route)
	b.routes.Store(&routes)
	b.mu.Unlock()
}

func (b *routeBucket) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	routes := b.routes.Load()
	if routes == nil {
		b.router.serveNotFound(w, req)
		return
	}
	for i := range *routes {
		candidate := &(*routes)[i]
		if !candidate.route.match(req) {
			continue
		}
		if len(candidate.route.vars) == 0 && candidate.route.template == candidate.route.pattern {
			candidate.handler.ServeHTTP(w, req)
			return
		}
		match := &matchedRoute{route: &candidate.route}
		ctx := context.WithValue(req.Context(), routeContextKey{}, match)
		matchedReq := req.WithContext(ctx)
		candidate.route.setPathValues(matchedReq)
		candidate.handler.ServeHTTP(w, matchedReq)
		return
	}
	b.router.serveNotFound(w, req)
}

type headerRoute struct {
	key     string
	value   string
	handler http.Handler
}

type routeMux struct {
	mux                     *http.ServeMux
	mu                      sync.RWMutex
	buckets                 map[string]*routeBucket
	methods                 map[string]struct{}
	routes                  []RouteInfo
	headers                 []headerRoute
	notFoundHandler         http.Handler
	methodNotAllowedHandler http.Handler
}

func newRouteMux() *routeMux {
	return &routeMux{
		mux:     http.NewServeMux(),
		buckets: make(map[string]*routeBucket),
		methods: make(map[string]struct{}),
	}
}

func (r *routeMux) handle(method, template string, handler http.Handler, walk bool) {
	compiled, err := compileRoute(template)
	if err != nil {
		panic(fmt.Sprintf("http: invalid route %q: %v", template, err))
	}
	r.handleCompiled(method, compiled, handler, walk)
}

func (r *routeMux) handleCompiled(method string, route compiledRoute, handler http.Handler, walk bool) {
	pattern := route.pattern
	if method != "*" {
		pattern = method + " " + pattern
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	bucket := r.buckets[pattern]
	if bucket == nil {
		bucket = &routeBucket{router: r}
		bucket.add(routeVariant{route: route, handler: handler})
		r.mux.Handle(pattern, bucket)
		r.buckets[pattern] = bucket
	} else {
		bucket.add(routeVariant{route: route, handler: handler})
	}
	if method != "*" {
		r.methods[method] = struct{}{}
	}
	if walk && method != "*" {
		r.routes = append(r.routes, RouteInfo{Method: method, Path: route.template})
	}
}

func (r *routeMux) handlePrefix(prefix string, handler http.Handler) {
	prefix = strings.TrimSuffix(prefix, "/")
	if prefix == "" {
		prefix = "/"
	}
	if prefix == "/" {
		r.handleCompiled("*", compiledRoute{pattern: "/", template: "/{path...}"}, handler, false)
		return
	}
	r.handle("*", prefix, handler, false)
	r.handle("*", prefix+"/{path...}", handler, false)
}

func (r *routeMux) handleHeader(key, value string, handler http.Handler) {
	r.mu.Lock()
	r.headers = append(r.headers, headerRoute{key: key, value: value, handler: handler})
	r.mu.Unlock()
}

func (r *routeMux) walk(fn WalkRouteFunc) error {
	r.mu.RLock()
	routes := slices.Clone(r.routes)
	r.mu.RUnlock()
	for _, route := range routes {
		if err := fn(route); err != nil {
			return err
		}
	}
	return nil
}

func (r *routeMux) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	headers := slices.Clone(r.headers)
	notFound := r.notFoundHandler
	methodNotAllowed := r.methodNotAllowedHandler
	r.mu.RUnlock()

	for _, route := range headers {
		if req.Header.Get(route.key) == route.value {
			route.handler.ServeHTTP(w, req)
			return
		}
	}

	if notFound == nil && methodNotAllowed == nil {
		r.mux.ServeHTTP(w, req)
		return
	}

	handler, pattern := r.mux.Handler(req)
	if pattern != "" {
		r.mux.ServeHTTP(w, req)
		return
	}
	r.mu.RLock()
	methods := make([]string, 0, len(r.methods))
	for method := range r.methods {
		methods = append(methods, method)
	}
	r.mu.RUnlock()
	methodNotAllowedMatch := matchesOtherMethod(r.mux, methods, req)
	if methodNotAllowedMatch && methodNotAllowed != nil {
		methodNotAllowed.ServeHTTP(w, req)
		return
	}
	if !methodNotAllowedMatch && notFound != nil {
		notFound.ServeHTTP(w, req)
		return
	}
	handler.ServeHTTP(w, req)
}

func matchesOtherMethod(mux *http.ServeMux, methods []string, req *http.Request) bool {
	for _, method := range methods {
		if method == req.Method || req.Method == http.MethodHead && method == http.MethodGet {
			continue
		}
		other := new(http.Request)
		*other = *req
		other.Method = method
		if _, pattern := mux.Handler(other); pattern != "" {
			return true
		}
	}
	return false
}

func (r *routeMux) serveNotFound(w http.ResponseWriter, req *http.Request) {
	r.mu.RLock()
	handler := r.notFoundHandler
	r.mu.RUnlock()
	if handler == nil {
		http.NotFound(w, req)
		return
	}
	handler.ServeHTTP(w, req)
}

func routeTemplate(req *http.Request) string {
	if route, ok := req.Context().Value(routeContextKey{}).(*matchedRoute); ok {
		return route.route.template
	}
	if req.Pattern != "" {
		if _, pattern, ok := strings.Cut(req.Pattern, " "); ok {
			return pattern
		}
		return req.Pattern
	}
	return req.URL.Path
}

func requestVars(req *http.Request) url.Values {
	route, ok := req.Context().Value(routeContextKey{}).(*matchedRoute)
	if !ok || len(route.route.vars) == 0 {
		return url.Values{}
	}
	return route.route.values(req)
}

func compileRoute(template string) (compiledRoute, error) {
	if template == "" || template[0] != '/' {
		return compiledRoute{}, fmt.Errorf("path must start with '/'")
	}
	if strings.ContainsAny(template, " \t") {
		return compiledRoute{}, fmt.Errorf("method and host patterns are not accepted here")
	}

	compiled := compiledRoute{template: template}
	var pattern strings.Builder
	captureIndex := 0
	for cursor := 0; cursor < len(template); {
		openRel := strings.IndexByte(template[cursor:], '{')
		if openRel < 0 {
			pattern.WriteString(template[cursor:])
			break
		}
		open := cursor + openRel
		pattern.WriteString(template[cursor:open])
		if open == 0 || template[open-1] != '/' {
			return compiledRoute{}, fmt.Errorf("variables must occupy a complete path segment")
		}
		closeRel := strings.IndexByte(template[open+1:], '}')
		if closeRel < 0 {
			return compiledRoute{}, fmt.Errorf("unclosed path variable")
		}
		closing := open + 1 + closeRel
		content := template[open+1 : closing]
		if content == "$" {
			pattern.WriteString("{$}")
			cursor = closing + 1
			continue
		}

		next := closing + 1
		suffix := ""
		if next < len(template) && template[next] == ':' {
			if strings.ContainsRune(template[next:], '/') {
				return compiledRoute{}, fmt.Errorf("custom verbs must terminate the path")
			}
			suffix = template[next:]
			next = len(template)
		} else if next < len(template) && template[next] != '/' {
			return compiledRoute{}, fmt.Errorf("variables must occupy a complete path segment")
		}

		variable, valueTemplate, legacyPattern, err := parseRouteVariable(content)
		if err != nil {
			return compiledRoute{}, err
		}
		segments := strings.Split(valueTemplate, "/")
		parts := make([]routePart, 0, len(segments))
		for i, segment := range segments {
			if i > 0 {
				pattern.WriteByte('/')
			}
			switch segment {
			case "*":
				name := fmt.Sprintf("%s%d", internalPathValuePrefix, captureIndex)
				captureIndex++
				pattern.WriteByte('{')
				pattern.WriteString(name)
				pattern.WriteByte('}')
				parts = append(parts, routePart{pathValue: name})
			case "**":
				if i != len(segments)-1 || (next < len(template) && template[next] == '/') {
					return compiledRoute{}, fmt.Errorf("multi-segment wildcard must terminate the path")
				}
				name := fmt.Sprintf("%s%d", internalPathValuePrefix, captureIndex)
				captureIndex++
				pattern.WriteByte('{')
				pattern.WriteString(name)
				pattern.WriteString("...}")
				parts = append(parts, routePart{pathValue: name})
			default:
				if segment == "" || strings.ContainsAny(segment, "{}") {
					return compiledRoute{}, fmt.Errorf("invalid path segment %q", segment)
				}
				pattern.WriteString(segment)
				parts = append(parts, routePart{literal: segment})
			}
		}
		if suffix != "" {
			last := &parts[len(parts)-1]
			if last.pathValue == "" {
				pattern.WriteString(suffix)
			} else {
				last.trimSuffix = suffix
			}
		}
		compiled.vars = append(compiled.vars, routeVariable{
			name:     variable,
			parts:    parts,
			validate: legacyPattern,
		})
		cursor = next
	}

	compiled.pattern = pattern.String()
	if compiled.pattern == "/" {
		compiled.pattern = "/{$}"
	} else if strings.HasSuffix(compiled.pattern, "/") {
		compiled.pattern += "{$}"
	}
	return compiled, nil
}

func parseRouteVariable(content string) (name, valueTemplate string, validate *regexp.Regexp, err error) {
	if before, after, ok := strings.Cut(content, "="); ok {
		name = strings.TrimSpace(before)
		valueTemplate = after
	} else if before, after, ok := strings.Cut(content, ":"); ok {
		name = strings.TrimSpace(before)
		if regexContainsPathSeparator(after) {
			return "", "", nil, fmt.Errorf("legacy regular expressions may not match multiple path segments")
		}
		validate, err = regexp.Compile("^(?:" + after + ")$")
		if err != nil {
			return "", "", nil, fmt.Errorf("invalid regular expression: %w", err)
		}
		valueTemplate = "*"
	} else if strings.HasSuffix(content, "...") {
		name = strings.TrimSuffix(content, "...")
		valueTemplate = "**"
	} else {
		name = content
		valueTemplate = "*"
	}
	if !validFieldPath(name) {
		return "", "", nil, fmt.Errorf("invalid path variable name %q", name)
	}
	if strings.HasPrefix(name, internalPathValuePrefix) {
		return "", "", nil, fmt.Errorf("path variable name %q uses a reserved prefix", name)
	}
	if valueTemplate == "" {
		return "", "", nil, fmt.Errorf("path variable %q has an empty template", name)
	}
	return name, valueTemplate, validate, nil
}

func regexContainsPathSeparator(pattern string) bool {
	inClass := false
	escaped := false
	for _, r := range pattern {
		if escaped {
			escaped = false
			continue
		}
		switch r {
		case '\\':
			escaped = true
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				return true
			}
		}
	}
	return false
}

func validFieldPath(name string) bool {
	for i, field := range strings.Split(name, ".") {
		if field == "" {
			return false
		}
		for j, r := range field {
			if j == 0 {
				if r != '_' && !unicode.IsLetter(r) {
					return false
				}
			} else if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				return false
			}
		}
		if i > 0 && field == "" {
			return false
		}
	}
	return true
}
