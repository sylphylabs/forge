package http

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

var benchmarkProtoJSON []byte

func TestProtoJSONFieldProjection(t *testing.T) {
	message := newProjectionMessage(t)
	fields := message.Descriptor().Fields()
	message.Set(fields.ByName("count"), protoreflect.ValueOfInt64(9_007_199_254_740_993))
	message.Mutable(fields.ByName("tags")).List().Append(protoreflect.ValueOfString("a"))
	message.Mutable(fields.ByName("tags")).List().Append(protoreflect.ValueOfString("b"))
	message.Mutable(fields.ByName("labels")).Map().Set(
		protoreflect.ValueOfString("env").MapKey(),
		protoreflect.ValueOfUint64(9_007_199_254_740_994),
	)
	message.Set(fields.ByName("data"), protoreflect.ValueOfBytes([]byte{0xfb, 0xff}))
	message.Set(fields.ByName("score"), protoreflect.ValueOfFloat64(math.Inf(1)))
	message.Set(fields.ByName("state"), protoreflect.ValueOfEnum(1))
	child := message.Mutable(fields.ByName("child")).Message()
	child.Set(child.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("child-1"))
	children := message.Mutable(fields.ByName("children")).List()
	childElement := children.NewElement().Message()
	childElement.Set(childElement.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("child-2"))
	children.Append(protoreflect.ValueOfMessage(childElement))

	tests := []struct {
		field string
		want  string
	}{
		{field: "count", want: `"9007199254740993"`},
		{field: "tags", want: `["a","b"]`},
		{field: "labels", want: `{"env":"9007199254740994"}`},
		{field: "data", want: `"+/8="`},
		{field: "score", want: `"Infinity"`},
		{field: "state", want: `"ACTIVE"`},
		{field: "child", want: `{"id":"child-1"}`},
		{field: "children", want: `[{"id":"child-2"}]`},
	}
	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got, err := json.Marshal(NewProtoJSONField(message.Interface(), tt.field))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("projected JSON = %s, want %s", got, tt.want)
			}

			decoded := newProjectionMessage(t)
			if err := json.Unmarshal(got, NewProtoJSONField(decoded.Interface(), tt.field)); err != nil {
				t.Fatal(err)
			}
			roundTrip, err := json.Marshal(NewProtoJSONField(decoded.Interface(), tt.field))
			if err != nil {
				t.Fatal(err)
			}
			if string(roundTrip) != tt.want {
				t.Fatalf("decoded field %q = %s, want %s", tt.field, roundTrip, tt.want)
			}
		})
	}
}

func TestProtoJSONOmitsAndClearsPathFields(t *testing.T) {
	message := newProjectionMessage(t)
	fields := message.Descriptor().Fields()
	message.Set(fields.ByName("name"), protoreflect.ValueOfString("publishers/1"))
	child := message.Mutable(fields.ByName("child")).Message()
	child.Set(child.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("child-1"))
	message.Set(fields.ByName("count"), protoreflect.ValueOfInt64(42))

	got, err := json.Marshal(NewProtoJSON(message.Interface(), "name", "child.id"))
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(got, &object); err != nil {
		t.Fatal(err)
	}
	if _, ok := object["name"]; ok {
		t.Fatalf("path field name was encoded: %s", got)
	}
	var encodedChild map[string]json.RawMessage
	if err := json.Unmarshal(object["child"], &encodedChild); err != nil {
		t.Fatal(err)
	}
	if _, ok := encodedChild["id"]; ok {
		t.Fatalf("nested path field child.id was encoded: %s", got)
	}
	if string(object["count"]) != `"42"` {
		t.Fatalf("count = %s, want quoted ProtoJSON int64", object["count"])
	}

	decoded := newProjectionMessage(t)
	if err := json.Unmarshal([]byte(`{"name":"body-name","child":{"id":"body-child"},"count":"7"}`), NewProtoJSON(decoded.Interface(), "name", "child.id")); err != nil {
		t.Fatal(err)
	}
	decodedFields := decoded.Descriptor().Fields()
	if decoded.Has(decodedFields.ByName("name")) || decoded.Get(decodedFields.ByName("name")).String() != "" {
		t.Fatal("top-level path field was not cleared after decoding")
	}
	decodedChild := decoded.Get(decodedFields.ByName("child")).Message()
	if decodedChild.Has(decodedChild.Descriptor().Fields().ByName("id")) || decodedChild.Get(decodedChild.Descriptor().Fields().ByName("id")).String() != "" {
		t.Fatal("nested path field was not cleared after decoding")
	}
	if got := decoded.Get(decodedFields.ByName("count")).Int(); got != 7 {
		t.Fatalf("count = %d, want 7", got)
	}
}

func BenchmarkProtoJSONOmitFields(b *testing.B) {
	message := newProjectionMessage(b)
	fields := message.Descriptor().Fields()
	message.Set(fields.ByName("name"), protoreflect.ValueOfString("publishers/1"))
	message.Set(fields.ByName("count"), protoreflect.ValueOfInt64(42))
	message.Mutable(fields.ByName("tags")).List().Append(protoreflect.ValueOfString("tag"))
	child := message.Mutable(fields.ByName("child")).Message()
	child.Set(child.Descriptor().Fields().ByName("id"), protoreflect.ValueOfString("child-1"))
	value := NewProtoJSON(message.Interface(), "name", "count", "child.id")

	b.ReportAllocs()
	for b.Loop() {
		data, err := json.Marshal(value)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkProtoJSON = data
	}
}

func TestProtoJSONFieldUnmarshalPreservesOtherFields(t *testing.T) {
	message := newProjectionMessage(t)
	if err := json.Unmarshal([]byte(`"first"`), NewProtoJSONField(message.Interface(), "name")); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`"42"`), NewProtoJSONField(message.Interface(), "count")); err != nil {
		t.Fatal(err)
	}
	fields := message.Descriptor().Fields()
	if got := message.Get(fields.ByName("name")).String(); got != "first" {
		t.Fatalf("name = %q, want first", got)
	}
	if got := message.Get(fields.ByName("count")).Int(); got != 42 {
		t.Fatalf("count = %d, want 42", got)
	}
}

func TestBuildPathRejectsUnsupportedQueryFields(t *testing.T) {
	message := newProjectionMessage(t).Interface()
	if _, err := BuildPath("/v1", message, WithQueryParams()); err == nil || !strings.Contains(err.Error(), `field "labels" is a map`) {
		t.Fatalf("map query error = %v", err)
	}
	if _, err := BuildPath("/v1", message, WithQueryParams(), WithOmitFields("labels")); err == nil || !strings.Contains(err.Error(), `field "children" is a repeated message`) {
		t.Fatalf("repeated message query error = %v", err)
	}
}

func newProjectionMessage(t testing.TB) protoreflect.Message {
	t.Helper()
	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	repeated := descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	stringKind := descriptorpb.FieldDescriptorProto_TYPE_STRING
	int64Kind := descriptorpb.FieldDescriptorProto_TYPE_INT64
	uint64Kind := descriptorpb.FieldDescriptorProto_TYPE_UINT64
	messageKind := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	bytesKind := descriptorpb.FieldDescriptorProto_TYPE_BYTES
	doubleKind := descriptorpb.FieldDescriptorProto_TYPE_DOUBLE
	enumKind := descriptorpb.FieldDescriptorProto_TYPE_ENUM
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Syntax:  proto.String("proto3"),
		Name:    proto.String("projection.proto"),
		Package: proto.String("openkratos.test"),
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Child"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("id"), Number: proto.Int32(1), Label: &optional, Type: &stringKind},
				},
			},
			{
				Name: proto.String("Projection"),
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name:    proto.String("LabelsEntry"),
						Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
						Field: []*descriptorpb.FieldDescriptorProto{
							{Name: proto.String("key"), Number: proto.Int32(1), Label: &optional, Type: &stringKind},
							{Name: proto.String("value"), Number: proto.Int32(2), Label: &optional, Type: &uint64Kind},
						},
					},
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					{Name: proto.String("name"), Number: proto.Int32(1), Label: &optional, Type: &stringKind},
					{Name: proto.String("count"), Number: proto.Int32(2), Label: &optional, Type: &int64Kind},
					{Name: proto.String("tags"), Number: proto.Int32(3), Label: &repeated, Type: &stringKind},
					{Name: proto.String("labels"), Number: proto.Int32(4), Label: &repeated, Type: &messageKind, TypeName: proto.String(".openkratos.test.Projection.LabelsEntry")},
					{Name: proto.String("child"), Number: proto.Int32(5), Label: &optional, Type: &messageKind, TypeName: proto.String(".openkratos.test.Child")},
					{Name: proto.String("data"), Number: proto.Int32(6), Label: &optional, Type: &bytesKind},
					{Name: proto.String("score"), Number: proto.Int32(7), Label: &optional, Type: &doubleKind},
					{Name: proto.String("state"), Number: proto.Int32(8), Label: &optional, Type: &enumKind, TypeName: proto.String(".openkratos.test.State")},
					{Name: proto.String("children"), Number: proto.Int32(9), Label: &repeated, Type: &messageKind, TypeName: proto.String(".openkratos.test.Child")},
				},
			},
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{
			{
				Name: proto.String("State"),
				Value: []*descriptorpb.EnumValueDescriptorProto{
					{Name: proto.String("STATE_UNSPECIFIED"), Number: proto.Int32(0)},
					{Name: proto.String("ACTIVE"), Number: proto.Int32(1)},
				},
			},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dynamicpb.NewMessage(file.Messages().ByName("Projection"))
}
