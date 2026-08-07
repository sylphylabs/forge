package httputil

import (
	"strings"
)

const (
	baseContentType = "application"

	jsonSubtype     = "json"
	jsonContentType = baseContentType + "/" + jsonSubtype
)

// ContentType returns the content-type with base prefix.
func ContentType(subtype string) string {
	return baseContentType + "/" + subtype
}

// ContentSubtype returns the content-subtype for the given content-type. The
// given content-type must be a valid content-type that starts with
// but no content-subtype will be returned.
// according rfc7231.
// contentType is assumed to be lowercase already.
func ContentSubtype(contentType string) string {
	if contentType == jsonContentType {
		return jsonSubtype
	}
	left := strings.IndexByte(contentType, '/')
	if left == -1 {
		return ""
	}
	right := strings.IndexByte(contentType, ';')
	if right == -1 {
		right = len(contentType)
	}
	if right < left {
		return ""
	}
	return contentType[left+1 : right]
}
