package json

import (
	"encoding/json"
	"reflect"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/encoding"
)

// Name is the name registered for the json codec.
const Name = "json"

var (
	// marshalOptions pins the wire-format settings for all protojson responses produced by
	// this codec. Keeping this unexported prevents any imported package from silently
	// changing the serialization behavior of unrelated code running in the same process.
	marshalOptions = protojson.MarshalOptions{
		EmitUnpopulated: true,
	}
	// unmarshalOptions pins the wire-format settings for all protojson parsing done by
	// this codec. Keeping this unexported prevents any imported package from silently
	// changing the deserialization behavior of unrelated code running in the same process.
	unmarshalOptions = protojson.UnmarshalOptions{
		DiscardUnknown: true,
	}
)

func init() {
	encoding.RegisterCodec(codec{})
}

// codec is a Codec implementation with json.
type codec struct{}

func (codec) Marshal(v any) ([]byte, error) {
	switch m := v.(type) {
	case json.Marshaler:
		return m.MarshalJSON()
	case proto.Message:
		return marshalOptions.Marshal(m)
	default:
		return json.Marshal(m)
	}
}

func (codec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	switch m := v.(type) {
	case json.Unmarshaler:
		return m.UnmarshalJSON(data)
	case proto.Message:
		return unmarshalOptions.Unmarshal(data, m)
	default:
		rv := reflect.ValueOf(v)
		for rv := rv; rv.Kind() == reflect.Pointer; {
			if rv.IsNil() {
				rv.Set(reflect.New(rv.Type().Elem()))
			}
			rv = rv.Elem()
		}
		if m, ok := reflect.Indirect(rv).Interface().(proto.Message); ok {
			return unmarshalOptions.Unmarshal(data, m)
		}
		return json.Unmarshal(data, m)
	}
}

func (codec) Name() string {
	return Name
}
