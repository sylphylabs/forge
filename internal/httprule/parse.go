package httprule

import (
	"fmt"
	"net/url"
	"strings"
)

// Parse parses a Google HTTP path template.
func Parse(pattern string) (*Template, error) {
	if pattern == "" || pattern[0] != '/' {
		return nil, syntaxError(0, "path must start with '/'")
	}
	path, verbRaw, verbOffset, err := splitVerb(pattern)
	if err != nil {
		return nil, err
	}
	verbDecoded := ""
	if verbRaw != "" {
		verbDecoded, err = decodeLiteral(verbRaw, verbOffset)
		if err != nil {
			return nil, err
		}
	}
	if path != "/" && strings.HasSuffix(path, "/") {
		return nil, syntaxError(len(path)-1, "trailing slash is not a path segment")
	}

	rawParts, offsets, err := splitTemplatePath(path)
	if err != nil {
		return nil, err
	}
	t := &Template{pattern: pattern, verbRaw: verbRaw, verbDecoded: verbDecoded}
	seenFields := make(map[string]struct{})
	for i, raw := range rawParts {
		part, variable, err := parsePart(raw, offsets[i])
		if err != nil {
			return nil, err
		}
		if variable != nil {
			if _, exists := seenFields[variable.FieldPath]; exists {
				return nil, syntaxError(offsets[i]+1, fmt.Sprintf("duplicate field path %q", variable.FieldPath))
			}
			seenFields[variable.FieldPath] = struct{}{}
			part.variable = len(t.variables)
			t.variables = append(t.variables, *variable)
		}
		if hasMulti(part.segments) && i != len(rawParts)-1 {
			return nil, syntaxError(offsets[i], "multi-segment wildcard must terminate the path")
		}
		t.parts = append(t.parts, part)
	}
	t.serveMuxPattern = buildServeMuxPattern(t.parts, verbRaw)
	return t, nil
}

func splitVerb(pattern string) (path, verb string, verbOffset int, err error) {
	depth := 0
	lastSlash := 0
	for i := 1; i < len(pattern); i++ {
		switch pattern[i] {
		case '{':
			if depth != 0 {
				return "", "", 0, syntaxError(i, "nested variables are not allowed")
			}
			depth = 1
		case '}':
			if depth == 0 {
				return "", "", 0, syntaxError(i, "unexpected '}'")
			}
			depth = 0
		case '/':
			if depth == 0 {
				lastSlash = i
			}
		case ':':
			if depth == 0 && i > lastSlash {
				if i == len(pattern)-1 {
					return "", "", 0, syntaxError(i, "custom verb is empty")
				}
				if strings.ContainsRune(pattern[i+1:], '/') {
					return "", "", 0, syntaxError(i, "custom verb must terminate the path")
				}
				return pattern[:i], pattern[i+1:], i + 1, nil
			}
		}
	}
	if depth != 0 {
		return "", "", 0, syntaxError(len(pattern), "unclosed variable")
	}
	return pattern, "", 0, nil
}

func splitTemplatePath(path string) ([]string, []int, error) {
	if path == "/" {
		return nil, nil, nil
	}
	var parts []string
	var offsets []int
	depth := 0
	start := 1
	for i := 1; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' && depth == 0 {
			if i == start {
				return nil, nil, syntaxError(i, "empty path segment")
			}
			parts = append(parts, path[start:i])
			offsets = append(offsets, start)
			start = i + 1
			continue
		}
		switch path[i] {
		case '{':
			if depth != 0 {
				return nil, nil, syntaxError(i, "nested variables are not allowed")
			}
			depth = 1
		case '}':
			if depth == 0 {
				return nil, nil, syntaxError(i, "unexpected '}'")
			}
			depth = 0
		}
	}
	if depth != 0 {
		return nil, nil, syntaxError(len(path), "unclosed variable")
	}
	return parts, offsets, nil
}

func parsePart(raw string, offset int) (pathPart, *Variable, error) {
	if strings.HasPrefix(raw, "{") || strings.HasSuffix(raw, "}") {
		if len(raw) < 3 || raw[0] != '{' || raw[len(raw)-1] != '}' {
			return pathPart{}, nil, syntaxError(offset, "variable must occupy a complete path segment")
		}
		content := raw[1 : len(raw)-1]
		fieldPath, valueTemplate, found := strings.Cut(content, "=")
		if !found {
			fieldPath = content
			valueTemplate = "*"
		}
		if !validFieldPath(fieldPath) {
			return pathPart{}, nil, syntaxError(offset+1, fmt.Sprintf("invalid field path %q", fieldPath))
		}
		if strings.ContainsRune(valueTemplate, '=') || valueTemplate == "" {
			return pathPart{}, nil, syntaxError(offset+1+len(fieldPath), "invalid variable template")
		}
		segments, err := parseSegments(valueTemplate, offset+2+len(fieldPath))
		if err != nil {
			return pathPart{}, nil, err
		}
		variable := &Variable{
			FieldPath: fieldPath,
			Template:  valueTemplate,
			Multi:     len(segments) > 1 || hasMulti(segments),
		}
		return pathPart{segments: segments}, variable, nil
	}

	segments, err := parseSegments(raw, offset)
	if err != nil {
		return pathPart{}, nil, err
	}
	if len(segments) != 1 {
		return pathPart{}, nil, syntaxError(offset, "invalid path segment")
	}
	return pathPart{segments: segments, variable: -1, unbound: segments[0].kind != literalSegment}, nil, nil
}

func parseSegments(raw string, offset int) ([]segment, error) {
	rawSegments := strings.Split(raw, "/")
	segments := make([]segment, 0, len(rawSegments))
	for i, rawSegment := range rawSegments {
		segmentOffset := offset
		for j := 0; j < i; j++ {
			segmentOffset += len(rawSegments[j]) + 1
		}
		if rawSegment == "" {
			return nil, syntaxError(segmentOffset, "empty variable segment")
		}
		switch rawSegment {
		case "*":
			segments = append(segments, segment{kind: singleWildcardSegment})
		case "**":
			if i != len(rawSegments)-1 {
				return nil, syntaxError(segmentOffset, "multi-segment wildcard must be last")
			}
			segments = append(segments, segment{kind: multiWildcardSegment})
		default:
			decoded, err := decodeLiteral(rawSegment, segmentOffset)
			if err != nil {
				return nil, err
			}
			segments = append(segments, segment{kind: literalSegment, raw: rawSegment, decoded: decoded})
		}
	}
	return segments, nil
}

func decodeLiteral(raw string, offset int) (string, error) {
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if isUnreserved(c) {
			continue
		}
		if c != '%' || i+2 >= len(raw) || !isHex(raw[i+1]) || !isHex(raw[i+2]) {
			return "", syntaxError(offset+i, fmt.Sprintf("reserved character %q must be percent-encoded", c))
		}
		i += 2
	}
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return "", syntaxError(offset, "invalid percent escape")
	}
	return decoded, nil
}

func buildServeMuxPattern(parts []pathPart, verb string) string {
	if len(parts) == 0 {
		return "/{$}"
	}
	var builder strings.Builder
	capture := 0
	lastKind := literalSegment
	for _, part := range parts {
		for _, segment := range part.segments {
			builder.WriteByte('/')
			lastKind = segment.kind
			switch segment.kind {
			case literalSegment:
				builder.WriteString(segment.raw)
			case singleWildcardSegment:
				fmt.Fprintf(&builder, "{%s%d}", pathValuePrefix, capture)
				capture++
			case multiWildcardSegment:
				fmt.Fprintf(&builder, "{%s%d...}", pathValuePrefix, capture)
				capture++
			}
		}
	}
	if verb != "" && lastKind == literalSegment {
		builder.WriteByte(':')
		builder.WriteString(verb)
	}
	return builder.String()
}

func hasMulti(segments []segment) bool {
	return len(segments) > 0 && segments[len(segments)-1].kind == multiWildcardSegment
}

func validFieldPath(path string) bool {
	if path == "" {
		return false
	}
	for _, field := range strings.Split(path, ".") {
		if field == "" || !isIdentStart(field[0]) {
			return false
		}
		for i := 1; i < len(field); i++ {
			if !isIdentContinue(field[i]) {
				return false
			}
		}
	}
	return true
}

func isIdentStart(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isIdentContinue(c byte) bool {
	return isIdentStart(c) || '0' <= c && c <= '9'
}

func syntaxError(offset int, reason string) error {
	return &SyntaxError{Offset: offset, Reason: reason}
}
