package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ProtoJSON adapts a protobuf message, or one of its top-level fields, to the
// standard encoding/json interfaces while retaining protobuf JSON semantics.
type ProtoJSON struct {
	message    proto.Message
	field      string
	omitFields []string
}

// NewProtoJSON adapts an entire protobuf message to JSON. The optional field
// paths are removed from the encoded value and cleared after decoding.
func NewProtoJSON(message proto.Message, omitFields ...string) *ProtoJSON {
	return &ProtoJSON{message: message, omitFields: append([]string(nil), omitFields...)}
}

// NewProtoJSONField adapts one top-level protobuf field to JSON.
func NewProtoJSONField(message proto.Message, field string) *ProtoJSON {
	return &ProtoJSON{message: message, field: field}
}

// MarshalJSON implements json.Marshaler.
func (p *ProtoJSON) MarshalJSON() ([]byte, error) {
	message, err := p.reflectedMessage()
	if err != nil {
		return nil, err
	}
	marshalMessage := p.message
	var projectedField protoreflect.FieldDescriptor
	if p.field != "" {
		projectedField, err = topLevelField(message.Descriptor(), p.field)
		if err != nil {
			return nil, err
		}
		projected := message.New()
		if message.Has(projectedField) || !projectedField.HasPresence() {
			projected.Set(projectedField, message.Get(projectedField))
		}
		marshalMessage = projected.Interface()
	}
	data, err := (protojson.MarshalOptions{EmitUnpopulated: true}).Marshal(marshalMessage)
	if err != nil {
		return nil, fmt.Errorf("marshal protobuf JSON: %w", err)
	}
	if p.field != "" {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, fmt.Errorf("project protobuf JSON field %q: %w", p.field, err)
		}
		value, ok := object[projectedField.JSONName()]
		if !ok {
			return []byte("null"), nil
		}
		return value, nil
	}
	if len(p.omitFields) == 0 {
		return data, nil
	}
	for _, fieldPath := range p.omitFields {
		data, err = omitProtoJSONField(data, message.Descriptor(), fieldPath)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (p *ProtoJSON) UnmarshalJSON(data []byte) error {
	message, err := p.reflectedMessage()
	if err != nil {
		return err
	}
	if p.field != "" {
		field, err := topLevelField(message.Descriptor(), p.field)
		if err != nil {
			return err
		}
		name, err := json.Marshal(field.JSONName())
		if err != nil {
			return fmt.Errorf("encode protobuf JSON field name %q: %w", p.field, err)
		}
		wrapped := make([]byte, 0, len(name)+len(data)+3)
		wrapped = append(wrapped, '{')
		wrapped = append(wrapped, name...)
		wrapped = append(wrapped, ':')
		wrapped = append(wrapped, data...)
		wrapped = append(wrapped, '}')
		projected := message.New()
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(wrapped, projected.Interface()); err != nil {
			return fmt.Errorf("unmarshal protobuf JSON field %q: %w", p.field, err)
		}
		if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
			message.Clear(field)
		} else {
			message.Set(field, projected.Get(field))
		}
		return nil
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, p.message); err != nil {
		return fmt.Errorf("unmarshal protobuf JSON: %w", err)
	}
	for _, fieldPath := range p.omitFields {
		if err := clearProtoField(message, fieldPath); err != nil {
			return err
		}
	}
	return nil
}

func (p *ProtoJSON) reflectedMessage() (protoreflect.Message, error) {
	if p == nil || isNilProtoMessage(p.message) {
		return nil, errors.New("protobuf JSON message is nil")
	}
	return p.message.ProtoReflect(), nil
}

func isNilProtoMessage(message proto.Message) bool {
	if message == nil {
		return true
	}
	value := reflect.ValueOf(message)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func topLevelField(message protoreflect.MessageDescriptor, name string) (protoreflect.FieldDescriptor, error) {
	if name == "" || strings.ContainsRune(name, '.') {
		return nil, fmt.Errorf("protobuf JSON field %q is not top-level", name)
	}
	field := message.Fields().ByName(protoreflect.Name(name))
	if field == nil {
		field = message.Fields().ByJSONName(name)
	}
	if field == nil {
		return nil, fmt.Errorf("protobuf JSON field %q does not exist in %s", name, message.FullName())
	}
	return field, nil
}

func omitProtoJSONField(data []byte, message protoreflect.MessageDescriptor, fieldPath string) ([]byte, error) {
	parts := strings.Split(fieldPath, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("protobuf JSON omit field path is empty")
	}
	return omitProtoJSONPath(data, message, parts, fieldPath)
}

func omitProtoJSONPath(data []byte, message protoreflect.MessageDescriptor, parts []string, fieldPath string) ([]byte, error) {
	field := message.Fields().ByName(protoreflect.Name(parts[0]))
	if field == nil {
		field = message.Fields().ByJSONName(parts[0])
	}
	if field == nil {
		return nil, fmt.Errorf("protobuf JSON omit field %q does not exist", fieldPath)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, fmt.Errorf("omit protobuf JSON field %q: %w", fieldPath, err)
	}
	name := field.JSONName()
	if len(parts) == 1 {
		delete(object, name)
		return json.Marshal(object)
	}
	if field.IsList() || field.IsMap() || field.Message() == nil {
		return nil, fmt.Errorf("protobuf JSON omit field %q has non-message prefix %q", fieldPath, parts[0])
	}
	value, ok := object[name]
	if !ok {
		return data, nil
	}
	value, err := omitProtoJSONPath(value, field.Message(), parts[1:], fieldPath)
	if err != nil {
		return nil, err
	}
	object[name] = value
	return json.Marshal(object)
}

func clearProtoField(message protoreflect.Message, fieldPath string) error {
	parts := strings.Split(fieldPath, ".")
	if len(parts) == 0 || parts[0] == "" {
		return errors.New("protobuf JSON clear field path is empty")
	}
	for i, part := range parts {
		field := message.Descriptor().Fields().ByName(protoreflect.Name(part))
		if field == nil {
			field = message.Descriptor().Fields().ByJSONName(part)
		}
		if field == nil {
			return fmt.Errorf("protobuf JSON clear field %q does not exist", strings.Join(parts[:i+1], "."))
		}
		if i == len(parts)-1 {
			message.Clear(field)
			return nil
		}
		if field.IsList() || field.IsMap() || field.Message() == nil {
			return fmt.Errorf("protobuf JSON clear field %q is not a singular message", strings.Join(parts[:i+1], "."))
		}
		if !message.Has(field) {
			return nil
		}
		message = message.Get(field).Message()
	}
	return nil
}
