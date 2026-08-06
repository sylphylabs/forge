package httprule

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
)

const pathValuePrefix = "__openkratos"

var (
	ErrPathMismatch    = errors.New("http rule path does not match template")
	ErrUnboundWildcard = errors.New("http rule contains an unbound wildcard")
)

// SyntaxError describes an invalid byte in an HTTP path template.
type SyntaxError struct {
	Offset int
	Reason string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("http rule syntax error at byte %d: %s", e.Offset, e.Reason)
}

// Variable describes one protobuf field referenced by a path template.
type Variable struct {
	FieldPath string
	Template  string
	Multi     bool
}

type segmentKind uint8

const (
	literalSegment segmentKind = iota
	singleWildcardSegment
	multiWildcardSegment
)

type segment struct {
	kind    segmentKind
	raw     string
	decoded string
}

type pathPart struct {
	segments []segment
	variable int
	unbound  bool
}

// Template is an immutable parsed Google HTTP path template.
type Template struct {
	pattern         string
	serveMuxPattern string
	matchKey        string
	parts           []pathPart
	variables       []Variable
	verbRaw         string
	verbDecoded     string
}

// Pattern returns the original Google HTTP path template.
func (t *Template) Pattern() string { return t.pattern }

// ServeMuxPattern returns the structural net/http.ServeMux path pattern.
func (t *Template) ServeMuxPattern() string { return t.serveMuxPattern }

// MatchKey returns a canonical description of the request paths this template matches.
func (t *Template) MatchKey() string { return t.matchKey }

// HasUnboundWildcard reports whether expansion needs a value not owned by a field.
func (t *Template) HasUnboundWildcard() bool {
	for _, part := range t.parts {
		if part.unbound {
			return true
		}
	}
	return false
}

// HasCustomVerb reports whether the template ends with a custom verb.
func (t *Template) HasCustomVerb() bool { return t.verbRaw != "" }

// Variables returns a copy of the variables referenced by the template.
func (t *Template) Variables() []Variable { return slices.Clone(t.variables) }

// Expand expands the template with canonical protobuf field strings.
func (t *Template) Expand(resolve func(fieldPath string) (string, error)) (string, error) {
	if resolve == nil {
		return "", errors.New("http rule: nil field resolver")
	}
	var path strings.Builder
	path.Grow(len(t.pattern))
	for _, part := range t.parts {
		if part.unbound {
			return "", fmt.Errorf("%w in template %q", ErrUnboundWildcard, t.pattern)
		}
		if part.variable < 0 {
			appendPathSegment(&path, part.segments[0].raw)
			continue
		}

		variable := t.variables[part.variable]
		value, err := resolve(variable.FieldPath)
		if err != nil {
			return "", fmt.Errorf("expand %q variable %q: %w", t.pattern, variable.FieldPath, err)
		}
		if err := appendExpandedVariable(&path, part.segments, value); err != nil {
			return "", fmt.Errorf("expand %q variable %q: %w", t.pattern, variable.FieldPath, err)
		}
	}
	if path.Len() == 0 {
		path.WriteByte('/')
	}
	if t.verbRaw != "" {
		path.WriteByte(':')
		path.WriteString(t.verbRaw)
	}
	return path.String(), nil
}

func appendPathSegment(path *strings.Builder, value string) {
	path.WriteByte('/')
	path.WriteString(value)
}

func appendExpandedVariable(path *strings.Builder, segments []segment, value string) error {
	if len(segments) == 1 {
		switch segments[0].kind {
		case literalSegment:
			if value != segments[0].decoded {
				return fmt.Errorf("value %q does not match literal %q", value, segments[0].decoded)
			}
			appendPathSegment(path, segments[0].raw)
			return nil
		case singleWildcardSegment:
			if value == "" {
				return errors.New("single-segment value is empty")
			}
			appendPathSegment(path, escapeSegment(value))
			return nil
		case multiWildcardSegment:
			for offset := 0; ; {
				part, next, ok := nextPathSegment(value, offset)
				if !ok {
					break
				}
				appendPathSegment(path, escapeSegment(part))
				offset = next
			}
			return nil
		}
	}

	valueOffset := 0
	for _, segment := range segments {
		switch segment.kind {
		case literalSegment:
			part, next, ok := nextPathSegment(value, valueOffset)
			if !ok || part != segment.decoded {
				return fmt.Errorf("value %q does not match resource template", value)
			}
			appendPathSegment(path, segment.raw)
			valueOffset = next
		case singleWildcardSegment:
			part, next, ok := nextPathSegment(value, valueOffset)
			if !ok || part == "" {
				return fmt.Errorf("value %q does not match resource template", value)
			}
			appendPathSegment(path, escapeSegment(part))
			valueOffset = next
		case multiWildcardSegment:
			for {
				part, next, ok := nextPathSegment(value, valueOffset)
				if !ok {
					break
				}
				appendPathSegment(path, escapeSegment(part))
				valueOffset = next
			}
		}
	}
	if _, _, ok := nextPathSegment(value, valueOffset); ok {
		return fmt.Errorf("value %q does not match resource template", value)
	}
	return nil
}

func nextPathSegment(value string, offset int) (segment string, next int, ok bool) {
	if offset > len(value) {
		return "", offset, false
	}
	if end := strings.IndexByte(value[offset:], '/'); end >= 0 {
		end += offset
		return value[offset:end], end + 1, true
	}
	return value[offset:], len(value) + 1, true
}

// ExtractValues recovers public variable values in Variables order from an
// escaped request path.
func (t *Template) ExtractValues(escapedPath string) ([]string, error) {
	if escapedPath == "" || escapedPath[0] != '/' {
		return nil, fmt.Errorf("extract %q: %w", t.pattern, ErrPathMismatch)
	}
	rawPath := escapedPath[1:]
	if t.verbDecoded != "" {
		if rawPath == "" {
			return nil, fmt.Errorf("extract %q: missing custom verb: %w", t.pattern, ErrPathMismatch)
		}
		lastPart := strings.LastIndexByte(rawPath, '/') + 1
		prefix, ok, err := stripEscapedSuffix(rawPath[lastPart:], ":"+t.verbDecoded)
		if err != nil {
			return nil, fmt.Errorf("extract %q: %w", t.pattern, err)
		}
		if !ok {
			return nil, fmt.Errorf("extract %q: custom verb mismatch: %w", t.pattern, ErrPathMismatch)
		}
		rawPath = rawPath[:lastPart+len(prefix)]
	}

	values := make([]string, len(t.variables))
	pathOffset := 0
	for _, part := range t.parts {
		captureStart := pathOffset
		captureEnd := pathOffset
		for _, expected := range part.segments {
			if expected.kind == multiWildcardSegment {
				captureEnd = len(rawPath)
				pathOffset = len(rawPath) + 1
				continue
			}
			raw, next, ok := nextExtractPathSegment(rawPath, pathOffset)
			if !ok {
				return nil, fmt.Errorf("extract %q: too few path segments: %w", t.pattern, ErrPathMismatch)
			}
			if expected.kind == singleWildcardSegment && raw == "" {
				return nil, fmt.Errorf("extract %q: empty wildcard segment: %w", t.pattern, ErrPathMismatch)
			}
			if expected.kind == literalSegment {
				decoded, err := url.PathUnescape(raw)
				if err != nil {
					return nil, fmt.Errorf("extract %q: invalid escape: %w", t.pattern, err)
				}
				if decoded != expected.decoded {
					return nil, fmt.Errorf("extract %q: literal mismatch: %w", t.pattern, ErrPathMismatch)
				}
			}
			captureEnd = pathOffset + len(raw)
			pathOffset = next
		}
		if part.variable >= 0 {
			variable := t.variables[part.variable]
			raw := rawPath[captureStart:captureEnd]
			var (
				value string
				err   error
			)
			if variable.Multi {
				value, err = decodeMulti(raw)
			} else {
				value, err = url.PathUnescape(raw)
			}
			if err != nil {
				return nil, fmt.Errorf("extract %q variable %q: %w", t.pattern, variable.FieldPath, err)
			}
			values[part.variable] = value
		}
	}
	if _, _, ok := nextExtractPathSegment(rawPath, pathOffset); ok {
		return nil, fmt.Errorf("extract %q: too many path segments: %w", t.pattern, ErrPathMismatch)
	}
	return values, nil
}

func nextExtractPathSegment(path string, offset int) (segment string, next int, ok bool) {
	if path == "" && offset == 0 {
		return "", 0, false
	}
	return nextPathSegment(path, offset)
}

// Extract recovers public variable values from an escaped request path.
func (t *Template) Extract(escapedPath string) (map[string]string, error) {
	extracted, err := t.ExtractValues(escapedPath)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(t.variables))
	for i, variable := range t.variables {
		values[variable.FieldPath] = extracted[i]
	}
	return values, nil
}
