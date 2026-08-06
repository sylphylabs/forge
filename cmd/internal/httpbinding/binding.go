package httpbinding

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/sylphylabs/forge/internal/httprule"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Binding is one normalized google.api.http rule for an RPC method.
type Binding struct {
	Index             int
	Method            string
	Path              string
	Body              string
	ResponseBody      string
	Template          *httprule.Template
	BodyField         protoreflect.FieldDescriptor
	ResponseBodyField protoreflect.FieldDescriptor
}

// Analyze returns the primary binding followed by each additional binding.
func Analyze(method protoreflect.MethodDescriptor) ([]*Binding, bool, error) {
	rule, ok := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	if !ok || rule == nil {
		return nil, false, nil
	}

	primary, err := analyzeRule(method, rule, 0)
	if err != nil {
		return nil, true, err
	}
	bindings := make([]*Binding, 1, 1+len(rule.AdditionalBindings))
	bindings[0] = primary
	for i, additional := range rule.AdditionalBindings {
		if len(additional.AdditionalBindings) != 0 {
			return nil, true, fmt.Errorf("additional binding %d: nested additional bindings are not allowed", i+1)
		}
		binding, err := analyzeRule(method, additional, i+1)
		if err != nil {
			return nil, true, fmt.Errorf("additional binding %d: %w", i+1, err)
		}
		bindings = append(bindings, binding)
	}
	return bindings, true, nil
}

// HasRule reports whether a method declares google.api.http.
func HasRule(method protoreflect.MethodDescriptor) bool {
	rule, ok := proto.GetExtension(method.Options(), annotations.E_Http).(*annotations.HttpRule)
	return ok && rule != nil
}

func analyzeRule(method protoreflect.MethodDescriptor, rule *annotations.HttpRule, index int) (*Binding, error) {
	httpMethod, path, err := methodAndPath(rule)
	if err != nil {
		return nil, err
	}
	template, err := httprule.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("path %q: %w", path, err)
	}
	if err := validatePathFields(method.Input(), template.Variables()); err != nil {
		return nil, fmt.Errorf("path %q: %w", path, err)
	}

	binding := &Binding{
		Index:        index,
		Method:       httpMethod,
		Path:         path,
		Body:         rule.Body,
		ResponseBody: rule.ResponseBody,
		Template:     template,
	}
	if binding.Body != "" && binding.Body != "*" {
		if strings.ContainsRune(binding.Body, '.') {
			return nil, fmt.Errorf("body field %q must be top-level", binding.Body)
		}
		binding.BodyField = method.Input().Fields().ByName(protoreflect.Name(binding.Body))
		if binding.BodyField == nil {
			return nil, fmt.Errorf("body field %q does not exist in request %s", binding.Body, method.Input().FullName())
		}
		for _, variable := range template.Variables() {
			if variable.FieldPath == binding.Body || strings.HasPrefix(variable.FieldPath, binding.Body+".") {
				return nil, fmt.Errorf("body field %q overlaps path field %q", binding.Body, variable.FieldPath)
			}
		}
	}
	if err := validateQueryFields(method.Input(), template.Variables(), binding.Body); err != nil {
		return nil, fmt.Errorf("query parameters: %w", err)
	}

	if binding.ResponseBody == "*" {
		return nil, errors.New("response_body \"*\" is invalid; omit response_body for the whole response")
	}
	if binding.ResponseBody != "" {
		if strings.ContainsRune(binding.ResponseBody, '.') {
			return nil, fmt.Errorf("response_body field %q must be top-level", binding.ResponseBody)
		}
		binding.ResponseBodyField = method.Output().Fields().ByName(protoreflect.Name(binding.ResponseBody))
		if binding.ResponseBodyField == nil {
			return nil, fmt.Errorf("response_body field %q does not exist in response %s", binding.ResponseBody, method.Output().FullName())
		}
	}
	return binding, nil
}

func methodAndPath(rule *annotations.HttpRule) (string, string, error) {
	var method, path string
	switch pattern := rule.Pattern.(type) {
	case *annotations.HttpRule_Get:
		method, path = http.MethodGet, pattern.Get
	case *annotations.HttpRule_Put:
		method, path = http.MethodPut, pattern.Put
	case *annotations.HttpRule_Post:
		method, path = http.MethodPost, pattern.Post
	case *annotations.HttpRule_Delete:
		method, path = http.MethodDelete, pattern.Delete
	case *annotations.HttpRule_Patch:
		method, path = http.MethodPatch, pattern.Patch
	case *annotations.HttpRule_Custom:
		if pattern.Custom == nil || pattern.Custom.Kind == "" {
			return "", "", errors.New("custom HTTP method is empty")
		}
		method, path = pattern.Custom.Kind, pattern.Custom.Path
	}
	if method == "" {
		return "", "", errors.New("HTTP method is not specified")
	}
	if path == "" {
		return "", "", errors.New("HTTP path is empty")
	}
	return method, path, nil
}

func validatePathFields(message protoreflect.MessageDescriptor, variables []httprule.Variable) error {
	for _, variable := range variables {
		fields := message.Fields()
		parts := strings.Split(variable.FieldPath, ".")
		for i, part := range parts {
			field := fields.ByName(protoreflect.Name(part))
			if field == nil {
				return fmt.Errorf("path field %q does not exist", strings.Join(parts[:i+1], "."))
			}
			if i < len(parts)-1 {
				if field.IsList() || field.IsMap() || field.Kind() != protoreflect.MessageKind && field.Kind() != protoreflect.GroupKind {
					return fmt.Errorf("path field %q is not a singular message", strings.Join(parts[:i+1], "."))
				}
				fields = field.Message().Fields()
				continue
			}
			if field.IsList() || field.IsMap() {
				return fmt.Errorf("path field %q is repeated or mapped", variable.FieldPath)
			}
			if field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind {
				return fmt.Errorf("path field %q is a message", variable.FieldPath)
			}
		}
	}
	return nil
}

func validateQueryFields(message protoreflect.MessageDescriptor, variables []httprule.Variable, body string) error {
	if body == "*" {
		return nil
	}
	pathFields := make(map[string]struct{}, len(variables))
	for _, variable := range variables {
		pathFields[variable.FieldPath] = struct{}{}
	}
	return validateQueryMessage(message, "", body, pathFields)
}

func validateQueryMessage(message protoreflect.MessageDescriptor, prefix, body string, pathFields map[string]struct{}) error {
	fields := message.Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		name := string(field.Name())
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if _, bound := pathFields[path]; bound || body != "" && (path == body || strings.HasPrefix(path, body+".")) {
			continue
		}
		if field.IsMap() {
			return fmt.Errorf("field %q is a map and cannot be encoded as a query parameter", path)
		}
		if field.IsList() && (field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind) {
			return fmt.Errorf("field %q is a repeated message and cannot be encoded as a query parameter", path)
		}
		if !field.IsList() && (field.Kind() == protoreflect.MessageKind || field.Kind() == protoreflect.GroupKind) && !isScalarQueryMessage(field.Message()) {
			if err := validateQueryMessage(field.Message(), path, body, pathFields); err != nil {
				return err
			}
		}
	}
	return nil
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
