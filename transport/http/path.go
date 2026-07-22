package http

import (
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/openkratos/kratos/encoding/form"
	"github.com/openkratos/kratos/internal/httprule"
)

var (
	// ErrUnspecifiedHTTPMethod indicates that a rule cannot select one client method.
	ErrUnspecifiedHTTPMethod = stderrors.New("http method is unspecified")
	// ErrUnboundPathWildcard indicates that a template wildcard has no request field.
	ErrUnboundPathWildcard = stderrors.New("http path template contains an unbound wildcard")
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

// BuildPath builds an HTTP request path from a Google HTTP template and request message.
func BuildPath(pathTemplate string, msg proto.Message, opts ...BuildPathOption) (string, error) {
	template, err := httprule.Parse(pathTemplate)
	if err != nil {
		return "", fmt.Errorf("build HTTP path: %w", err)
	}
	options := buildPathOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	if isNilMessage(msg) {
		if len(template.Variables()) > 0 {
			return "", stderrors.New("build HTTP path: request message is nil")
		}
		path, err := template.Expand(func(string) (string, error) {
			return "", stderrors.New("request message is nil")
		})
		if err != nil {
			return "", mapPathError(err)
		}
		return path, nil
	}

	path, err := template.Expand(func(fieldPath string) (string, error) {
		return pathFieldText(msg.ProtoReflect(), fieldPath)
	})
	if err != nil {
		return "", mapPathError(err)
	}
	if !options.queryParams {
		return path, nil
	}
	pathFields := make(map[string]struct{}, len(template.Variables()))
	for _, variable := range template.Variables() {
		pathFields[variable.FieldPath] = struct{}{}
	}
	if err := validateQueryParameters(msg.ProtoReflect().Descriptor(), "", "", pathFields, options.omitFields); err != nil {
		return "", fmt.Errorf("build HTTP query: %w", err)
	}

	queryParams, err := form.EncodeValues(msg)
	if err != nil {
		return "", fmt.Errorf("build HTTP query: %w", err)
	}
	for _, variable := range template.Variables() {
		delete(queryParams, encodedFieldPath(msg, variable.FieldPath))
	}
	if len(queryParams) > 0 {
		omitQueryParams(queryParams, options.omitFields)
		if query := queryParams.Encode(); query != "" {
			path += "?" + query
		}
	}
	return path, nil
}

func validateQueryParameters(message protoreflect.MessageDescriptor, protoPrefix, jsonPrefix string, pathFields map[string]struct{}, omitFields []string) error {
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		name := string(field.Name())
		protoPath := name
		if protoPrefix != "" {
			protoPath = protoPrefix + "." + name
		}
		jsonPath := field.JSONName()
		if jsonPrefix != "" {
			jsonPath = jsonPrefix + "." + field.JSONName()
		}
		if _, bound := pathFields[protoPath]; bound || omittedQueryField(protoPath, jsonPath, omitFields) {
			continue
		}
		if field.IsMap() {
			return fmt.Errorf("field %q is a map and cannot be encoded as a query parameter", protoPath)
		}
		if field.IsList() && (field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind) {
			return fmt.Errorf("field %q is a repeated message and cannot be encoded as a query parameter", protoPath)
		}
		if !field.IsList() && (field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind) && !isScalarQueryMessage(field.Message()) {
			if err := validateQueryParameters(field.Message(), protoPath, jsonPath, pathFields, omitFields); err != nil {
				return err
			}
		}
	}
	return nil
}

func omittedQueryField(protoPath, jsonPath string, omitted []string) bool {
	for _, field := range omitted {
		if protoPath == field || strings.HasPrefix(protoPath, field+".") || jsonPath == field || strings.HasPrefix(jsonPath, field+".") {
			return true
		}
	}
	return false
}

func isScalarQueryMessage(message protoreflect.MessageDescriptor) bool {
	switch message.FullName() {
	case "google.protobuf.Timestamp", "google.protobuf.Duration", "google.protobuf.BytesValue",
		"google.protobuf.DoubleValue", "google.protobuf.FloatValue", "google.protobuf.Int64Value",
		"google.protobuf.Int32Value", "google.protobuf.UInt64Value", "google.protobuf.UInt32Value",
		"google.protobuf.BoolValue", "google.protobuf.StringValue", "google.protobuf.FieldMask",
		"google.protobuf.Value", "google.protobuf.Struct":
		return true
	default:
		return false
	}
}

func encodedFieldPath(msg proto.Message, fieldPath string) string {
	fields := msg.ProtoReflect().Descriptor().Fields()
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

func isNilMessage(msg proto.Message) bool {
	if msg == nil {
		return true
	}
	value := reflect.ValueOf(msg)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func mapPathError(err error) error {
	if stderrors.Is(err, httprule.ErrUnboundWildcard) {
		return fmt.Errorf("%w: %v", ErrUnboundPathWildcard, err)
	}
	return fmt.Errorf("build HTTP path: %w", err)
}

func pathFieldText(message protoreflect.Message, fieldPath string) (string, error) {
	parts := strings.Split(fieldPath, ".")
	for i, part := range parts {
		field := message.Descriptor().Fields().ByName(protoreflect.Name(part))
		if field == nil {
			return "", fmt.Errorf("field %q does not exist", strings.Join(parts[:i+1], "."))
		}
		if i < len(parts)-1 {
			if field.IsList() || field.IsMap() || field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
				return "", fmt.Errorf("field %q is not a singular message", strings.Join(parts[:i+1], "."))
			}
			if field.HasPresence() && !message.Has(field) {
				return "", fmt.Errorf("field %q is not set", strings.Join(parts[:i+1], "."))
			}
			message = message.Get(field).Message()
			continue
		}
		if field.IsList() || field.IsMap() {
			return "", fmt.Errorf("field %q is repeated or mapped", fieldPath)
		}
		if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
			return "", fmt.Errorf("field %q is a message", fieldPath)
		}
		if field.HasPresence() && !message.Has(field) {
			return "", fmt.Errorf("field %q is not set", fieldPath)
		}
		return formatPathScalar(field, message.Get(field))
	}
	return "", fmt.Errorf("field path %q is empty", fieldPath)
}

func formatPathScalar(field protoreflect.FieldDescriptor, value protoreflect.Value) (string, error) {
	switch field.Kind() {
	case protoreflect.BoolKind:
		return strconv.FormatBool(value.Bool()), nil
	case protoreflect.EnumKind:
		descriptor := field.Enum().Values().ByNumber(value.Enum())
		if descriptor == nil {
			return strconv.FormatInt(int64(value.Enum()), 10), nil
		}
		return string(descriptor.Name()), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return strconv.FormatInt(value.Int(), 10), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return strconv.FormatUint(value.Uint(), 10), nil
	case protoreflect.FloatKind:
		return formatPathFloat(value.Float(), 32), nil
	case protoreflect.DoubleKind:
		return formatPathFloat(value.Float(), 64), nil
	case protoreflect.StringKind:
		return value.String(), nil
	case protoreflect.BytesKind:
		return base64.StdEncoding.EncodeToString(value.Bytes()), nil
	default:
		return "", fmt.Errorf("field %q has unsupported kind %s", field.FullName(), field.Kind())
	}
}

func formatPathFloat(value float64, bits int) string {
	switch {
	case math.IsNaN(value):
		return "NaN"
	case math.IsInf(value, 1):
		return "Infinity"
	case math.IsInf(value, -1):
		return "-Infinity"
	default:
		return strconv.FormatFloat(value, 'g', -1, bits)
	}
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
