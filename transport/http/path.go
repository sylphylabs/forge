package http

import (
	"encoding/base64"
	stderrors "errors"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"

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

// CompiledPath is an immutable HTTP path template prepared for one protobuf
// request type. It moves template parsing and descriptor validation out of the
// request path.
type CompiledPath struct {
	template        *httprule.Template
	staticPath      string
	descriptor      protoreflect.MessageDescriptor
	fields          map[string]compiledPathField
	variables       []httprule.Variable
	queryOmitFields []string
	queryParams     bool
	prepareOnce     sync.Once
	prototype       proto.Message
	prepareOptions  buildPathOptions
	prepareErr      error
}

type compiledPathField struct {
	name   string
	fields []protoreflect.FieldDescriptor
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

// CompilePath parses an HTTP path template and validates it against prototype.
// The returned path is safe for concurrent use.
func CompilePath(pathTemplate string, prototype proto.Message, opts ...BuildPathOption) (*CompiledPath, error) {
	template, err := httprule.Parse(pathTemplate)
	if err != nil {
		return nil, fmt.Errorf("compile HTTP path: %w", err)
	}
	options := buildPathOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	return compilePath(template, prototype, options)
}

func compilePath(template *httprule.Template, prototype proto.Message, options buildPathOptions) (*CompiledPath, error) {
	variables := template.Variables()
	if isNilMessage(prototype) {
		if len(variables) > 0 || options.queryParams {
			return nil, stderrors.New("compile HTTP path: protobuf message prototype is nil")
		}
		path, err := template.Expand(func(string) (string, error) {
			return "", stderrors.New("unexpected path variable")
		})
		if err != nil {
			return nil, fmt.Errorf("compile HTTP path: %w", err)
		}
		return &CompiledPath{template: template, staticPath: path}, nil
	}

	descriptor := prototype.ProtoReflect().Descriptor()
	if descriptor == nil {
		return nil, stderrors.New("compile HTTP path: protobuf message descriptor is not initialized")
	}
	compiled := &CompiledPath{
		template:    template,
		descriptor:  descriptor,
		fields:      make(map[string]compiledPathField, len(variables)),
		variables:   variables,
		queryParams: options.queryParams,
	}
	if len(variables) == 0 && !template.HasUnboundWildcard() {
		path, err := template.Expand(func(string) (string, error) {
			return "", stderrors.New("unexpected path variable")
		})
		if err != nil {
			return nil, fmt.Errorf("compile HTTP path: %w", err)
		}
		compiled.staticPath = path
	}
	pathFields := make(map[string]struct{}, len(variables))
	for _, variable := range variables {
		field, err := compilePathField(descriptor, variable.FieldPath)
		if err != nil {
			return nil, fmt.Errorf("compile HTTP path variable %q: %w", variable.FieldPath, err)
		}
		compiled.fields[variable.FieldPath] = field
		pathFields[variable.FieldPath] = struct{}{}
		compiled.queryOmitFields = append(compiled.queryOmitFields, encodedDescriptorFieldPath(descriptor, variable.FieldPath))
	}
	if options.queryParams {
		if err := validateQueryParameters(descriptor, "", "", pathFields, options.omitFields); err != nil {
			return nil, fmt.Errorf("compile HTTP query: %w", err)
		}
		for _, field := range options.omitFields {
			compiled.queryOmitFields = append(compiled.queryOmitFields, encodedDescriptorFieldPath(descriptor, field))
		}
	}
	return compiled, nil
}

// MustCompilePath parses pathTemplate and defers descriptor preparation until
// the first Build call. This allows generated package-level declarations to be
// created before protobuf descriptors are initialized.
func MustCompilePath(pathTemplate string, prototype proto.Message, opts ...BuildPathOption) *CompiledPath {
	template, err := httprule.Parse(pathTemplate)
	if err != nil {
		panic(fmt.Errorf("compile HTTP path: %w", err))
	}
	options := buildPathOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	if isNilMessage(prototype) {
		compiled, err := compilePath(template, prototype, options)
		if err != nil {
			panic(err)
		}
		return compiled
	}
	options.omitFields = append([]string(nil), options.omitFields...)
	return &CompiledPath{
		template:       template,
		prototype:      prototype,
		prepareOptions: options,
	}
}

// Pattern returns the original HTTP path template.
func (p *CompiledPath) Pattern() string {
	if p == nil || p.template == nil {
		return ""
	}
	return p.template.Pattern()
}

// Build expands the compiled template with values from msg.
func (p *CompiledPath) Build(msg proto.Message) (string, error) {
	if p == nil || p.template == nil {
		return "", stderrors.New("build HTTP path: compiled path is nil")
	}
	if p.prototype != nil {
		p.prepareOnce.Do(func() {
			compiled, err := compilePath(p.template, p.prototype, p.prepareOptions)
			if err != nil {
				p.prepareErr = err
				return
			}
			p.staticPath = compiled.staticPath
			p.descriptor = compiled.descriptor
			p.fields = compiled.fields
			p.variables = compiled.variables
			p.queryOmitFields = compiled.queryOmitFields
			p.queryParams = compiled.queryParams
		})
		if p.prepareErr != nil {
			return "", p.prepareErr
		}
	}
	if isNilMessage(msg) {
		if len(p.variables) > 0 {
			return "", stderrors.New("build HTTP path: request message is nil")
		}
		if p.staticPath != "" {
			return p.staticPath, nil
		}
		path, err := p.template.Expand(func(string) (string, error) {
			return "", stderrors.New("request message is nil")
		})
		if err != nil {
			return "", mapPathError(err)
		}
		return path, nil
	}

	message := msg.ProtoReflect()
	if p.descriptor != nil && message.Descriptor() != p.descriptor {
		return "", fmt.Errorf(
			"build HTTP path: message type %q does not match compiled type %q",
			message.Descriptor().FullName(),
			p.descriptor.FullName(),
		)
	}
	path := p.staticPath
	if path == "" {
		var err error
		path, err = p.template.Expand(func(fieldPath string) (string, error) {
			field, ok := p.fields[fieldPath]
			if !ok {
				return "", fmt.Errorf("field %q was not compiled", fieldPath)
			}
			return field.text(message)
		})
		if err != nil {
			return "", mapPathError(err)
		}
	}
	if !p.queryParams {
		return path, nil
	}
	queryParams, err := form.EncodeValuesExcept(msg, p.queryOmitFields...)
	if err != nil {
		return "", fmt.Errorf("build HTTP query: %w", err)
	}
	if len(queryParams) > 0 {
		if query := queryParams.Encode(); query != "" {
			path += "?" + query
		}
	}
	return path, nil
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

	omitFields := make([]string, 0, len(template.Variables())+len(options.omitFields))
	for _, variable := range template.Variables() {
		omitFields = append(omitFields, encodedFieldPath(msg, variable.FieldPath))
	}
	for _, field := range options.omitFields {
		omitFields = append(omitFields, encodedDescriptorFieldPath(msg.ProtoReflect().Descriptor(), field))
	}
	queryParams, err := form.EncodeValuesExcept(msg, omitFields...)
	if err != nil {
		return "", fmt.Errorf("build HTTP query: %w", err)
	}
	if len(queryParams) > 0 {
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
	return encodedDescriptorFieldPath(msg.ProtoReflect().Descriptor(), fieldPath)
}

func encodedDescriptorFieldPath(message protoreflect.MessageDescriptor, fieldPath string) string {
	fields := message.Fields()
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

func compilePathField(message protoreflect.MessageDescriptor, fieldPath string) (compiledPathField, error) {
	parts := strings.Split(fieldPath, ".")
	compiled := compiledPathField{name: fieldPath, fields: make([]protoreflect.FieldDescriptor, 0, len(parts))}
	for i, part := range parts {
		field := message.Fields().ByName(protoreflect.Name(part))
		if field == nil {
			return compiledPathField{}, fmt.Errorf("field %q does not exist", strings.Join(parts[:i+1], "."))
		}
		compiled.fields = append(compiled.fields, field)
		if i < len(parts)-1 {
			if field.IsList() || field.IsMap() || field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
				return compiledPathField{}, fmt.Errorf("field %q is not a singular message", strings.Join(parts[:i+1], "."))
			}
			message = field.Message()
			continue
		}
		if field.IsList() || field.IsMap() {
			return compiledPathField{}, fmt.Errorf("field %q is repeated or mapped", fieldPath)
		}
		if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
			return compiledPathField{}, fmt.Errorf("field %q is a message", fieldPath)
		}
	}
	return compiled, nil
}

func (p compiledPathField) text(message protoreflect.Message) (string, error) {
	for i, field := range p.fields {
		if i < len(p.fields)-1 {
			if field.HasPresence() && !message.Has(field) {
				return "", fmt.Errorf("field %q is not set", p.name)
			}
			message = message.Get(field).Message()
			continue
		}
		if field.HasPresence() && !message.Has(field) {
			return "", fmt.Errorf("field %q is not set", p.name)
		}
		return formatPathScalar(field, message.Get(field))
	}
	return "", fmt.Errorf("field path %q is empty", p.name)
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
