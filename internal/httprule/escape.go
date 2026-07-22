package httprule

import (
	"fmt"
	"net/url"
	"strings"
)

const upperHex = "0123456789ABCDEF"

func escapeSegment(value string) string {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if isUnreserved(c) {
			builder.WriteByte(c)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(upperHex[c>>4])
		builder.WriteByte(upperHex[c&15])
	}
	return builder.String()
}

func escapeSegments(value string) []string {
	segments := strings.Split(value, "/")
	for i := range segments {
		segments[i] = escapeSegment(segments[i])
	}
	return segments
}

func decodeMulti(value string) (string, error) {
	var builder strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			builder.WriteByte(value[i])
			continue
		}
		if i+2 >= len(value) || !isHex(value[i+1]) || !isHex(value[i+2]) {
			return "", fmt.Errorf("invalid URL escape at byte %d", i)
		}
		decoded := unhex(value[i+1])<<4 | unhex(value[i+2])
		if decoded == '/' {
			builder.WriteString(value[i : i+3])
		} else {
			builder.WriteByte(decoded)
		}
		i += 2
	}
	return builder.String(), nil
}

func stripEscapedSuffix(raw, suffix string) (string, bool, error) {
	if _, err := url.PathUnescape(raw); err != nil {
		return "", false, err
	}
	for i := len(raw); i >= 0; i-- {
		if i > 0 && raw[i-1] == '%' || i > 1 && raw[i-2] == '%' {
			continue
		}
		decoded, err := url.PathUnescape(raw[i:])
		if err == nil && decoded == suffix {
			return raw[:i], true, nil
		}
	}
	return raw, false, nil
}

func isUnreserved(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case '0' <= c && c <= '9':
		return c - '0'
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10
	default:
		return c - 'A' + 10
	}
}
