package contracttest

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sylphylabs/forge/api/errors/v1"
	message "github.com/sylphylabs/forge/api/message/v1"
)

func TestErrorStatusRoundTrip(t *testing.T) {
	want := &errors.Status{
		Kind:    errors.Kind_KIND_NOT_FOUND,
		Domain:  "sylphy.test.v1",
		Reason:  "DOCUMENT_NOT_FOUND",
		Message: "document not found",
		Metadata: map[string]string{
			"name": "documents/42",
		},
		TraceId: "trace-42",
		Violations: []*errors.Violation{
			{Field: "name", Description: "must not be empty"},
		},
	}
	wire, err := proto.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got := new(errors.Status)
	if err = proto.Unmarshal(wire, got); err != nil {
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
	proto.SetExtension(enumOptions, errors.E_DefaultKind, errors.Kind_KIND_INTERNAL)
	if got := proto.GetExtension(enumOptions, errors.E_DefaultKind); got != errors.Kind_KIND_INTERNAL {
		t.Fatalf("default kind = %v, want KIND_INTERNAL", got)
	}

	valueOptions := new(descriptorpb.EnumValueOptions)
	proto.SetExtension(valueOptions, errors.E_Kind, errors.Kind_KIND_NOT_FOUND)
	if got := proto.GetExtension(valueOptions, errors.E_Kind); got != errors.Kind_KIND_NOT_FOUND {
		t.Fatalf("kind = %v, want KIND_NOT_FOUND", got)
	}
}

func TestMessageSubscriptionOption(t *testing.T) {
	options := new(descriptorpb.MethodOptions)
	proto.SetExtension(options, message.E_Subscribe, &message.Subscription{
		Destination: "order.created",
	})
	got, ok := proto.GetExtension(options, message.E_Subscribe).(*message.Subscription)
	if !ok {
		t.Fatalf("subscribe extension type = %T, want *message.Subscription", proto.GetExtension(options, message.E_Subscribe))
	}
	if got.GetDestination() != "order.created" {
		t.Fatalf("destination = %q, want %q", got.GetDestination(), "order.created")
	}
}

func TestExtensionAllocations(t *testing.T) {
	tests := []struct {
		message protoreflect.FullName
		number  protoreflect.FieldNumber
		name    protoreflect.FullName
	}{
		{"google.protobuf.EnumOptions", 500101, "sylphy.errors.v1.default_kind"},
		{"google.protobuf.EnumValueOptions", 500102, "sylphy.errors.v1.kind"},
		{"google.protobuf.MethodOptions", 500201, "sylphy.message.v1.subscribe"},
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
