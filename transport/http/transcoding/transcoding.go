// Package transcoding carries the Protobuf half of Forge's HTTP transport:
// Google HTTP transcoding, ProtoJSON projection, path and query binding, raw
// HTTP bodies, and stream body fields.
//
// It is separate from transport/http so that a service serving plain JSON does
// not link the Protobuf runtime it never calls. Importing this package installs
// the behavior into the transport; generated code does so on the application's
// behalf, and a hand-written service that binds Protobuf messages imports it
// directly:
//
//	import _ "github.com/sylphylabs/forge/transport/http/transcoding"
package transcoding

import (
	"fmt"
	"net/url"
	"reflect"

	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/sylphylabs/forge/encoding"
	"github.com/sylphylabs/forge/encoding/form"

	// The proto and protojson codecs must be registered for this runtime to
	// resolve them by name; importing them here means an application gets them
	// by importing this package alone.
	_ "github.com/sylphylabs/forge/encoding/proto"
	_ "github.com/sylphylabs/forge/encoding/protojson"
	transporthttp "github.com/sylphylabs/forge/transport/http"
)

func init() {
	transporthttp.RegisterSchema(runtime{})
}

// runtime is the Protobuf half of the HTTP transport, installed into
// transport/http by importing this package.
type runtime struct{}

// Owns reports whether v is a Protobuf message, or a pointer to one.
//
// Every other method may assume this returned true, which is why none of them
// repeats the check.
func (runtime) Owns(v any) bool {
	if _, ok := v.(proto.Message); ok {
		return true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return false
	}
	// A pointer to a message pointer is owned too: the transport allocates it
	// before decoding, so a nil target does not silently discard a body.
	if elem := rv.Type().Elem(); elem.Kind() == reflect.Pointer && elem.Implements(protoMessageType) {
		return true
	}
	return rv.Elem().IsValid() && rv.Elem().CanInterface() && isMessage(rv.Elem().Interface())
}

func isMessage(v any) bool {
	_, ok := v.(proto.Message)
	return ok
}

// BindPath binds path variables onto a message's declared fields.
func (runtime) BindPath(v any, vars []transporthttp.PathVar) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("http: path binding requires a proto.Message, got %T", v)
	}
	for _, variable := range vars {
		if err := form.DecodeValue(msg, variable.Name, variable.Value); err != nil {
			return err
		}
	}
	return nil
}

// BindValues binds URL values onto a message, which needs the declared field
// set to resolve names and repeated fields.
func (runtime) BindValues(v any, values url.Values) error {
	if m, ok := v.(proto.Message); ok {
		return form.DecodeValues(m, values)
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if m, ok := rv.Interface().(proto.Message); ok {
		return form.DecodeValues(m, values)
	}
	return fmt.Errorf("http: value binding requires a proto.Message, got %T", v)
}

// protoMessageType is the interface a pointer must implement to be decoded as a
// message rather than as a plain Go value.
var protoMessageType = reflect.TypeOf((*proto.Message)(nil)).Elem()

// Codec returns the codec to use for a message.
//
// The standard JSON codec is replaced by protojson, because encoding/json
// cannot read or write a message: a Duration, a Timestamp, any well-known
// type, and an int64 spelled as a string all differ between their Go and JSON
// forms, and encoding/json drops them without reporting anything.
//
// Any other codec is honored. Registering one is a deliberate act — a service
// that installs its own "application/x-thrift" means it, and this runtime has
// no standing to override that choice.
func (runtime) Codec(negotiated encoding.Codec, _ any) encoding.Codec {
	if negotiated.Name() != "json" {
		return negotiated
	}
	if protoJSON := encoding.GetCodec("protojson"); protoJSON != nil {
		return protoJSON
	}
	return negotiated
}

// Target allocates a nil message target so decoding into a pointer-to-pointer
// does not silently discard the body.
func (runtime) Target(v any) any {
	if _, ok := v.(proto.Message); ok {
		return v
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return v
	}
	elem := rv.Type().Elem()
	if elem.Kind() != reflect.Pointer || !elem.Implements(protoMessageType) {
		return v
	}
	target := rv.Elem()
	if target.IsNil() {
		target.Set(reflect.New(elem.Elem()))
	}
	return target.Interface()
}

// RawBody returns the raw payload carrier v represents.
func (runtime) RawBody(v any) (transporthttp.RawBody, bool) {
	switch body := v.(type) {
	case *httpbody.HttpBody:
		if body == nil {
			return nil, false
		}
		return rawBody{body}, true
	case **httpbody.HttpBody:
		if body == nil {
			return nil, false
		}
		if *body == nil {
			*body = new(httpbody.HttpBody)
		}
		return rawBody{*body}, true
	default:
		return nil, false
	}
}

// rawBody adapts the generated HttpBody to the transport's view of it.
type rawBody struct{ *httpbody.HttpBody }

func (b rawBody) SetContentType(contentType string) { b.HttpBody.ContentType = contentType }
func (b rawBody) SetData(data []byte)               { b.HttpBody.Data = data }

// DecodeField decodes one frame into a named message field.
//
// The generator only declares a body field for a singular message-kind field,
// so a mismatch here is a programming error and is reported rather than
// silently ignored.
func (runtime) DecodeField(v any, field string, read func(target any) error) error {
	pm, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("http: stream body field %q requires a proto.Message, got %T", field, v)
	}
	fd := pm.ProtoReflect().Descriptor().Fields().ByName(protoreflect.Name(field))
	if fd == nil || fd.Kind() != protoreflect.MessageKind || fd.IsList() || fd.IsMap() {
		return fmt.Errorf("http: stream body field %q is not a singular message field", field)
	}
	sub := pm.ProtoReflect().NewField(fd)
	if err := read(sub.Message().Interface()); err != nil {
		return err
	}
	pm.ProtoReflect().Set(fd, sub)
	return nil
}

// SupportPackageIsVersion1 is a compile-time assertion referenced by generated
// code. Referencing it is also what links this package, and with it the schema
// runtime the generated bindings need.
const SupportPackageIsVersion1 = true
