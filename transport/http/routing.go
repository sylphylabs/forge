package http

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/httprule"
	"github.com/sylphylabs/forge/transport"
)

const internalPathValuePrefix = "__forge"

type routeContextKey struct{}

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
	pattern          string
	muxPattern       string
	captureNames     []string
	template         string
	vars             []routeVariable
	httpRule         *httprule.Template
	directPathValues bool
}

func (r *compiledRoute) match(req *http.Request) ([]string, bool, error) {
	if r.httpRule != nil {
		if r.directPathValues {
			return nil, true, nil
		}
		values, err := r.httpRule.ExtractValues(req.URL.EscapedPath())
		if stderrors.Is(err, httprule.ErrPathMismatch) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		return values, true, nil
	}
	values := make([]string, len(r.vars))
	for i, variable := range r.vars {
		value, ok := variable.value(req)
		if !ok || variable.validate != nil && !variable.validate.MatchString(value) {
			return nil, false, nil
		}
		values[i] = value
	}
	return values, true, nil
}

func (r *compiledRoute) setPathValues(req *http.Request, values []string, captureNames []string) {
	if r.directPathValues {
		for i, variable := range r.vars {
			captureName := variable.parts[0].pathValue
			if i < len(captureNames) {
				captureName = captureNames[i]
			}
			if captureName != variable.name {
				req.SetPathValue(variable.name, req.PathValue(captureName))
			}
		}
	} else {
		for i, variable := range r.vars {
			req.SetPathValue(variable.name, values[i])
		}
	}
	for _, name := range captureNames {
		if strings.HasPrefix(name, internalPathValuePrefix) {
			req.SetPathValue(name, "")
		}
	}
}

func (r *compiledRoute) values(req *http.Request) url.Values {
	values := make(url.Values, len(r.vars))
	for _, variable := range r.vars {
		values[variable.name] = []string{req.PathValue(variable.name)}
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

type matchedRouteHandler interface {
	serveMatchedRoute(http.ResponseWriter, *http.Request, *compiledRoute, []string, []string)
}

type routeBucket struct {
	router       *routeMux
	captureNames []string
	mu           sync.Mutex
	routes       atomic.Pointer[[]routeVariant]
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
	// net/http publishes the structural mux pattern before invoking the bucket.
	// Do not expose it unless one of the registered route variants really matches.
	req.Pattern = ""
	routes := b.routes.Load()
	if routes == nil {
		b.router.serveNotFound(w, req)
		return
	}
	for i := range *routes {
		candidate := &(*routes)[i]
		if len(candidate.route.vars) == 0 && candidate.route.template == candidate.route.pattern {
			req.Pattern = candidate.route.template
			candidate.handler.ServeHTTP(w, req)
			return
		}
		values, ok, err := candidate.route.match(req)
		if err != nil {
			b.router.serveRouteError(w, req, err)
			return
		}
		if !ok {
			continue
		}
		req.Pattern = candidate.route.template
		if handler, ok := candidate.handler.(matchedRouteHandler); ok {
			handler.serveMatchedRoute(w, req, &candidate.route, values, b.captureNames)
			return
		}
		ctx := context.WithValue(req.Context(), routeContextKey{}, &candidate.route)
		matchedReq := req.WithContext(ctx)
		candidate.route.setPathValues(matchedReq, values, b.captureNames)
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

type routeRuntime struct {
	headers                 []headerRoute
	notFoundHandler         http.Handler
	methodNotAllowedHandler http.Handler
	errorEncoder            EncodeErrorFunc
}

type routeMux struct {
	mux        *http.ServeMux
	mu         sync.RWMutex
	buckets    map[string]*routeBucket
	methods    map[string]struct{}
	methodList atomic.Pointer[[]string]
	routes     []RouteInfo
	runtime    atomic.Pointer[routeRuntime]
}

func newRouteMux() *routeMux {
	router := &routeMux{
		mux:     http.NewServeMux(),
		buckets: make(map[string]*routeBucket),
		methods: make(map[string]struct{}),
	}
	router.runtime.Store(new(routeRuntime))
	return router
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
	muxPattern := route.muxPattern
	if muxPattern == "" {
		muxPattern = route.pattern
	}
	if method != "*" {
		pattern = method + " " + pattern
		muxPattern = method + " " + muxPattern
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	bucket := r.buckets[pattern]
	if bucket == nil {
		bucket = &routeBucket{router: r, captureNames: route.captureNames}
		bucket.add(routeVariant{route: route, handler: handler})
		r.mux.Handle(muxPattern, bucket)
		r.buckets[pattern] = bucket
	} else {
		bucket.add(routeVariant{route: route, handler: handler})
	}
	if method != "*" {
		if _, ok := r.methods[method]; !ok {
			r.methods[method] = struct{}{}
			current := r.methodList.Load()
			methods := make([]string, 0, 1)
			if current != nil {
				methods = make([]string, 0, len(*current)+1)
				methods = append(methods, (*current)...)
			}
			methods = append(methods, method)
			r.methodList.Store(&methods)
		}
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
	current := r.runtime.Load()
	next := *current
	next.headers = make([]headerRoute, len(current.headers), len(current.headers)+1)
	copy(next.headers, current.headers)
	next.headers = append(next.headers, headerRoute{key: key, value: value, handler: handler})
	r.runtime.Store(&next)
	r.mu.Unlock()
}

func (r *routeMux) setNotFoundHandler(handler http.Handler) {
	r.mu.Lock()
	next := *r.runtime.Load()
	next.notFoundHandler = handler
	r.runtime.Store(&next)
	r.mu.Unlock()
}

func (r *routeMux) setMethodNotAllowedHandler(handler http.Handler) {
	r.mu.Lock()
	next := *r.runtime.Load()
	next.methodNotAllowedHandler = handler
	r.runtime.Store(&next)
	r.mu.Unlock()
}

func (r *routeMux) setErrorEncoder(encoder EncodeErrorFunc) {
	r.mu.Lock()
	next := *r.runtime.Load()
	next.errorEncoder = encoder
	r.runtime.Store(&next)
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
	req.Pattern = ""
	runtime := r.runtime.Load()
	var headerHandler http.Handler
	for _, route := range runtime.headers {
		if req.Header.Get(route.key) == route.value {
			headerHandler = route.handler
			break
		}
	}
	notFound := runtime.notFoundHandler
	methodNotAllowed := runtime.methodNotAllowedHandler

	if headerHandler != nil {
		headerHandler.ServeHTTP(w, req)
		return
	}

	if notFound == nil && methodNotAllowed == nil {
		r.serveMuxHandler(w, req)
		return
	}

	handler, pattern := r.mux.Handler(req)
	if pattern != "" {
		if _, matchedBucket := handler.(*routeBucket); matchedBucket {
			r.mux.ServeHTTP(w, req)
		} else {
			handler.ServeHTTP(w, req)
		}
		return
	}
	methods := r.methodList.Load()
	var methodNotAllowedMatch bool
	if methods != nil {
		methodNotAllowedMatch = matchesOtherMethod(r.mux, *methods, req)
	}
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

func (r *routeMux) serveMuxHandler(w http.ResponseWriter, req *http.Request) {
	if req.RequestURI == "*" {
		r.mux.ServeHTTP(w, req)
		req.Pattern = ""
		return
	}
	handler, _ := r.mux.Handler(req)
	if _, matchedBucket := handler.(*routeBucket); matchedBucket {
		r.mux.ServeHTTP(w, req)
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
	handler := r.runtime.Load().notFoundHandler
	if handler == nil {
		http.NotFound(w, req)
		return
	}
	handler.ServeHTTP(w, req)
}

func (r *routeMux) serveRouteError(w http.ResponseWriter, req *http.Request, err error) {
	encode := r.runtime.Load().errorEncoder
	if encode == nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	var escape url.EscapeError
	if stderrors.As(err, &escape) {
		encode(w, req, ErrTranscoding.Msg(err.Error()).Wrap(err))
		return
	}
	encode(w, req, forgeerrors.MustDefine(forgeerrors.KindInternal, forgeerrors.Domain, "HTTP_TRANSCODING").Msg(err.Error()).Wrap(err))
}

func routeTemplate(req *http.Request) string {
	if route, ok := routeFromRequest(req); ok {
		return route.template
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
	route, ok := routeFromRequest(req)
	if !ok || len(route.vars) == 0 {
		return url.Values{}
	}
	return route.values(req)
}

func routeFromRequest(req *http.Request) (*compiledRoute, bool) {
	if route, ok := req.Context().Value(routeContextKey{}).(*compiledRoute); ok {
		return route, true
	}
	tr, ok := transport.FromServerContext(req.Context())
	if !ok {
		return nil, false
	}
	httpTransport, ok := tr.(*Transport)
	if !ok || httpTransport.route == nil {
		return nil, false
	}
	return httpTransport.route, true
}

func compileRoute(template string) (compiledRoute, error) {
	if usesGoogleTemplate(template) {
		rule, err := httprule.Parse(template)
		if err != nil {
			return compiledRoute{}, err
		}
		variables := rule.Variables()
		compiled := compiledRoute{
			pattern:  rule.ServeMuxPattern(),
			template: template,
			vars:     make([]routeVariable, len(variables)),
			httpRule: rule,
		}
		compiled.muxPattern = compiled.pattern
		compiled.captureNames = serveMuxCaptureNames(compiled.pattern)
		compiled.directPathValues = len(variables) > 0 && !rule.HasUnboundWildcard() && !rule.HasCustomVerb()
		for i, variable := range variables {
			compiled.vars[i].name = variable.FieldPath
			if variable.Template != "*" || variable.Multi {
				compiled.directPathValues = false
				continue
			}
			compiled.vars[i].parts = []routePart{{pathValue: fmt.Sprintf("%s%d", internalPathValuePrefix, i)}}
		}
		if compiled.directPathValues {
			for i, variable := range variables {
				if !strings.ContainsRune(variable.FieldPath, '.') && i < len(compiled.captureNames) {
					capture := compiled.captureNames[i]
					compiled.muxPattern = strings.Replace(
						compiled.muxPattern,
						"{"+capture+"}",
						"{"+variable.FieldPath+"}",
						1,
					)
					compiled.captureNames[i] = variable.FieldPath
				}
			}
		}
		return compiled, nil
	}
	return compileLegacyRoute(template)
}

func compileLegacyRoute(template string) (compiledRoute, error) {
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
	compiled.muxPattern = compiled.pattern
	compiled.captureNames = serveMuxCaptureNames(compiled.pattern)
	return compiled, nil
}

func serveMuxCaptureNames(pattern string) []string {
	var names []string
	for cursor := 0; cursor < len(pattern); {
		open := strings.IndexByte(pattern[cursor:], '{')
		if open < 0 {
			break
		}
		open += cursor
		end := strings.IndexByte(pattern[open+1:], '}')
		if end < 0 {
			break
		}
		end += open + 1
		name := pattern[open+1 : end]
		name = strings.TrimSuffix(name, "...")
		if name != "$" {
			names = append(names, name)
		}
		cursor = end + 1
	}
	return names
}

func usesGoogleTemplate(template string) bool {
	for cursor := 0; cursor < len(template); {
		open := strings.IndexByte(template[cursor:], '{')
		if open < 0 {
			break
		}
		open += cursor
		end := strings.IndexByte(template[open+1:], '}')
		if end < 0 {
			return true
		}
		end += open + 1
		content := template[open+1 : end]
		if strings.ContainsRune(content, '=') || !strings.ContainsRune(content, ':') && !strings.HasSuffix(content, "...") {
			return true
		}
		cursor = end + 1
	}
	for _, segment := range strings.Split(template, "/") {
		if segment == "*" || segment == "**" {
			return true
		}
	}
	return false
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
