package contracttest

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/openkratos/api/errors/v1"
)

func TestErrorStatusRoundTrip(t *testing.T) {
	want := &errors.Status{
		Code:    404,
		Reason:  "DOCUMENT_NOT_FOUND",
		Message: "document not found",
		Metadata: map[string]string{
			"name": "documents/42",
		},
	}
	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got := new(errors.Status)
	if err := proto.Unmarshal(wire, got); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(got, want) {
		t.Fatalf("wire round trip = %v, want %v", got, want)
	}

	jsonWire, err := protojson.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	jsonGot := new(errors.Status)
	if err := protojson.Unmarshal(jsonWire, jsonGot); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(jsonGot, want) {
		t.Fatalf("JSON round trip = %v, want %v", jsonGot, want)
	}
}

func TestCustomOptions(t *testing.T) {
	enumOptions := new(descriptorpb.EnumOptions)
	proto.SetExtension(enumOptions, errors.E_DefaultCode, int32(500))
	if got := proto.GetExtension(enumOptions, errors.E_DefaultCode); got != int32(500) {
		t.Fatalf("default error code = %v, want 500", got)
	}
}

func TestExtensionAllocations(t *testing.T) {
	tests := []struct {
		message protoreflect.FullName
		number  protoreflect.FieldNumber
		name    protoreflect.FullName
	}{
		{"google.protobuf.EnumOptions", 500101, "openkratos.errors.v1.default_code"},
		{"google.protobuf.EnumValueOptions", 500102, "openkratos.errors.v1.code"},
	}
	for _, test := range tests {
		extension, err := protoregistry.GlobalTypes.FindExtensionByNumber(test.message, test.number)
		if err != nil {
			t.Errorf("find extension %s:%d: %v", test.message, test.number, err)
			continue
		}
		if got := extension.TypeDescriptor().FullName(); got != test.name {
			t.Errorf("extension %s:%d = %s, want %s", test.message, test.number, got, test.name)
		}
	}
}

func TestStandardIdempotency(t *testing.T) {
	options := &descriptorpb.MethodOptions{
		IdempotencyLevel: descriptorpb.MethodOptions_IDEMPOTENT.Enum(),
	}
	if got := options.GetIdempotencyLevel(); got != descriptorpb.MethodOptions_IDEMPOTENT {
		t.Fatalf("idempotency level = %v", got)
	}
}
