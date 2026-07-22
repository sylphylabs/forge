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
	parts           []pathPart
	variables       []Variable
	verbRaw         string
	verbDecoded     string
}

// Pattern returns the original Google HTTP path template.
func (t *Template) Pattern() string { return t.pattern }

// ServeMuxPattern returns the structural net/http.ServeMux path pattern.
func (t *Template) ServeMuxPattern() string { return t.serveMuxPattern }

// Variables returns a copy of the variables referenced by the template.
func (t *Template) Variables() []Variable { return slices.Clone(t.variables) }

// Expand expands the template with canonical protobuf field strings.
func (t *Template) Expand(resolve func(fieldPath string) (string, error)) (string, error) {
	if resolve == nil {
		return "", errors.New("http rule: nil field resolver")
	}
	parts := make([]string, 0, len(t.parts))
	for _, part := range t.parts {
		if part.unbound {
			return "", fmt.Errorf("%w in template %q", ErrUnboundWildcard, t.pattern)
		}
		if part.variable < 0 {
			parts = append(parts, part.segments[0].raw)
			continue
		}

		variable := t.variables[part.variable]
		value, err := resolve(variable.FieldPath)
		if err != nil {
			return "", fmt.Errorf("expand %q variable %q: %w", t.pattern, variable.FieldPath, err)
		}
		expanded, err := expandVariable(part.segments, value)
		if err != nil {
			return "", fmt.Errorf("expand %q variable %q: %w", t.pattern, variable.FieldPath, err)
		}
		parts = append(parts, expanded...)
	}
	path := "/" + strings.Join(parts, "/")
	if len(parts) == 0 {
		path = "/"
	}
	if t.verbRaw != "" {
		path += ":" + t.verbRaw
	}
	return path, nil
}

func expandVariable(segments []segment, value string) ([]string, error) {
	if len(segments) == 1 {
		switch segments[0].kind {
		case literalSegment:
			if value != segments[0].decoded {
				return nil, fmt.Errorf("value %q does not match literal %q", value, segments[0].decoded)
			}
			return []string{segments[0].raw}, nil
		case singleWildcardSegment:
			if value == "" {
				return nil, errors.New("single-segment value is empty")
			}
			return []string{escapeSegment(value)}, nil
		case multiWildcardSegment:
			return escapeSegments(value), nil
		}
	}

	values := strings.Split(value, "/")
	result := make([]string, 0, len(values))
	valueIndex := 0
	for _, segment := range segments {
		switch segment.kind {
		case literalSegment:
			if valueIndex >= len(values) || values[valueIndex] != segment.decoded {
				return nil, fmt.Errorf("value %q does not match resource template", value)
			}
			result = append(result, segment.raw)
			valueIndex++
		case singleWildcardSegment:
			if valueIndex >= len(values) || values[valueIndex] == "" {
				return nil, fmt.Errorf("value %q does not match resource template", value)
			}
			result = append(result, escapeSegment(values[valueIndex]))
			valueIndex++
		case multiWildcardSegment:
			for ; valueIndex < len(values); valueIndex++ {
				result = append(result, escapeSegment(values[valueIndex]))
			}
		}
	}
	if valueIndex != len(values) {
		return nil, fmt.Errorf("value %q does not match resource template", value)
	}
	return result, nil
}

// Extract recovers public variable values from an escaped request path.
func (t *Template) Extract(escapedPath string) (map[string]string, error) {
	if escapedPath == "" || escapedPath[0] != '/' {
		return nil, fmt.Errorf("extract %q: %w", t.pattern, ErrPathMismatch)
	}
	rawParts := splitPath(escapedPath)
	if t.verbDecoded != "" {
		if len(rawParts) == 0 {
			return nil, fmt.Errorf("extract %q: missing custom verb: %w", t.pattern, ErrPathMismatch)
		}
		prefix, ok, err := stripEscapedSuffix(rawParts[len(rawParts)-1], ":"+t.verbDecoded)
		if err != nil {
			return nil, fmt.Errorf("extract %q: %w", t.pattern, err)
		}
		if !ok {
			return nil, fmt.Errorf("extract %q: custom verb mismatch: %w", t.pattern, ErrPathMismatch)
		}
		rawParts[len(rawParts)-1] = prefix
	}

	captures := make([][]string, len(t.variables))
	pathIndex := 0
	for _, part := range t.parts {
		for _, expected := range part.segments {
			if expected.kind == multiWildcardSegment {
				if part.variable >= 0 {
					captures[part.variable] = append(captures[part.variable], rawParts[pathIndex:]...)
				}
				pathIndex = len(rawParts)
				continue
			}
			if pathIndex >= len(rawParts) {
				return nil, fmt.Errorf("extract %q: too few path segments: %w", t.pattern, ErrPathMismatch)
			}
			raw := rawParts[pathIndex]
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
			if part.variable >= 0 {
				captures[part.variable] = append(captures[part.variable], raw)
			}
			pathIndex++
		}
	}
	if pathIndex != len(rawParts) {
		return nil, fmt.Errorf("extract %q: too many path segments: %w", t.pattern, ErrPathMismatch)
	}

	values := make(map[string]string, len(t.variables))
	for i, variable := range t.variables {
		raw := strings.Join(captures[i], "/")
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
		values[variable.FieldPath] = value
	}
	return values, nil
}

func splitPath(path string) []string {
	if path == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}
