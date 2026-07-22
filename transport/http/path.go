package http

import (
	"net/url"
	"reflect"
	"regexp"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/openkratos/kratos/encoding/form"
)

var pathTemplateParamRE = regexp.MustCompile(`{([.\w]+)(=[^{}]*)?}`)

var escapedPathSegmentSubDelimiters = strings.NewReplacer(
	"%21", "!",
	"%24", "$",
	"%26", "&",
	"%27", "'",
	"%28", "(",
	"%29", ")",
	"%2A", "*",
	"%2B", "+",
	"%2C", ",",
	"%3B", ";",
	"%3D", "=",
)

// BuildPathOption configures path construction.
type BuildPathOption func(*buildPathOptions)

type buildPathOptions struct {
	queryParams bool
	omitFields  []string
}

// WithQueryParams appends request fields that are not bound in the path as query parameters.
func WithQueryParams() BuildPathOption {
	return func(o *buildPathOptions) {
		o.queryParams = true
	}
}

// WithOmitFields excludes fields from generated query parameters.
func WithOmitFields(fields ...string) BuildPathOption {
	return func(o *buildPathOptions) {
		o.omitFields = append(o.omitFields, fields...)
	}
}

// BuildPath builds an HTTP request path from a path template and request message.
func BuildPath(pathTemplate string, msg any, opts ...BuildPathOption) string {
	if msg == nil || (reflect.ValueOf(msg).Kind() == reflect.Pointer && reflect.ValueOf(msg).IsNil()) {
		return pathTemplate
	}

	options := buildPathOptions{}
	for _, opt := range opts {
		opt(&options)
	}

	queryParams, _ := form.EncodeValues(msg)
	pathParams := make(map[string]struct{})
	path := pathTemplate
	if strings.ContainsRune(pathTemplate, '{') {
		path = pathTemplateParamRE.ReplaceAllStringFunc(pathTemplate, func(in string) string {
			matches := pathTemplateParamRE.FindStringSubmatch(in)
			fieldPath := matches[1]
			queryKey := encodedFieldPath(msg, fieldPath)
			pathParams[queryKey] = struct{}{}
			valueTemplate := "*"
			if matches[2] != "" {
				valueTemplate = strings.TrimPrefix(matches[2], "=")
			}
			return escapePathValue(queryParams.Get(queryKey), valueTemplate)
		})
	}

	if !options.queryParams {
		if v, ok := msg.(proto.Message); ok {
			if query := form.EncodeFieldMask(v.ProtoReflect()); query != "" {
				return path + "?" + query
			}
		}
		return path
	}
	if len(queryParams) > 0 {
		for key := range pathParams {
			delete(queryParams, key)
		}
		omitQueryParams(queryParams, options.omitFields)
		if query := queryParams.Encode(); query != "" {
			path += "?" + query
		}
	}
	return path
}

func encodedFieldPath(msg any, fieldPath string) string {
	message, ok := msg.(proto.Message)
	if !ok {
		return fieldPath
	}
	fields := message.ProtoReflect().Descriptor().Fields()
	parts := strings.Split(fieldPath, ".")
	encoded := make([]string, 0, len(parts))
	for i, part := range parts {
		field := fields.ByName(protoreflect.Name(part))
		if field == nil {
			field = fields.ByJSONName(part)
		}
		if field == nil {
			return fieldPath
		}
		encoded = append(encoded, field.JSONName())
		if i == len(parts)-1 {
			break
		}
		if field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
			return fieldPath
		}
		fields = field.Message().Fields()
	}
	return strings.Join(encoded, ".")
}

func escapePathValue(value, valueTemplate string) string {
	if valueTemplate == "**" {
		return escapePathSegments(value)
	}
	if valueTemplate == "*" {
		return escapePathSegment(value)
	}

	templateSegments := strings.Split(valueTemplate, "/")
	valueSegments := strings.Split(value, "/")
	escaped := make([]string, 0, len(valueSegments))
	valueIndex := 0
	for templateIndex, segment := range templateSegments {
		switch segment {
		case "*":
			if valueIndex >= len(valueSegments) {
				return escapePathSegment(value)
			}
			escaped = append(escaped, escapePathSegment(valueSegments[valueIndex]))
			valueIndex++
		case "**":
			if templateIndex != len(templateSegments)-1 {
				return escapePathSegment(value)
			}
			escaped = append(escaped, escapePathSegments(strings.Join(valueSegments[valueIndex:], "/")))
			valueIndex = len(valueSegments)
		default:
			if valueIndex >= len(valueSegments) || valueSegments[valueIndex] != segment {
				return escapePathSegment(value)
			}
			escaped = append(escaped, segment)
			valueIndex++
		}
	}
	if valueIndex != len(valueSegments) {
		return escapePathSegment(value)
	}
	return strings.Join(escaped, "/")
}

func escapePathSegments(value string) string {
	segments := strings.Split(value, "/")
	for i := range segments {
		segments[i] = escapePathSegment(segments[i])
	}
	return strings.Join(segments, "/")
}

func escapePathSegment(value string) string {
	return escapedPathSegmentSubDelimiters.Replace(url.PathEscape(value))
}

func omitQueryParams(values map[string][]string, fields []string) {
	for _, field := range fields {
		if field == "" {
			continue
		}
		delete(values, field)
		prefix := field + "."
		for key := range values {
			if strings.HasPrefix(key, prefix) {
				delete(values, key)
			}
		}
	}
}
