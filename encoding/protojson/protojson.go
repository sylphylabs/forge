package protojson

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/encoding"
)

// Name is the name registered for the protojson codec.
const Name = "protojson"

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

// codec is a Codec implementation with protojson.
type codec struct{}

func (codec) Marshal(v any) ([]byte, error) {
	m, ok := v.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("failed to marshal, message is %T, want proto.Message", v)
	}
	return marshalOptions.Marshal(m)
}

func (codec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	m, ok := v.(proto.Message)
	if !ok {
		return fmt.Errorf("failed to unmarshal, message is %T, want proto.Message", v)
	}
	return unmarshalOptions.Unmarshal(data, m)
}

func (codec) Name() string {
	return Name
}
