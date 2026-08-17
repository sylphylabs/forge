package testv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sylphylabs/forge/api/errors/v1"
)

func TestErrorAnnotations(t *testing.T) {
	descriptor := FailureReason(0).Descriptor()
	value := descriptor.Values().ByName("FAILURE_REASON_NOT_FOUND")
	if value == nil {
		t.Fatal("FAILURE_REASON_NOT_FOUND descriptor is missing")
	}
	if got := proto.GetExtension(value.Options(), errors.E_Kind); got != errors.Kind_KIND_NOT_FOUND {
		t.Fatalf("error kind = %v, want %v", got, errors.Kind_KIND_NOT_FOUND)
	}
	if got := proto.GetExtension(descriptor.Options(), errors.E_DefaultKind); got != errors.Kind_KIND_INTERNAL {
		t.Fatalf("default error kind = %v, want %v", got, errors.Kind_KIND_INTERNAL)
	}
}

func TestStandardMethodOptions(t *testing.T) {
	service := File_test_v1_consumer_proto.Services().ByName("DocumentService")
	if service == nil {
		t.Fatal("DocumentService descriptor is missing")
	}
	method := service.Methods().ByName("GetDocument")
	if method == nil {
		t.Fatal("GetDocument descriptor is missing")
	}
	methodOptions := method.Options().(*descriptorpb.MethodOptions)
	if got := methodOptions.GetIdempotencyLevel(); got != descriptorpb.MethodOptions_IDEMPOTENT {
		t.Fatalf("idempotency level = %v", got)
	}
}

// TestThrowsDeclarations proves the application-side throws contract end to
// end through real protoc output: the typed extensions compile, the declared
// reasons read back through proto.GetExtension, and the
// (sylphy.errors.v1.throws) marker reads back as true from each extension
// field's own FieldOptions — which is exactly what a generator claims
// declarations by.
func TestThrowsDeclarations(t *testing.T) {
	service := File_test_v1_consumer_proto.Services().ByName("DocumentService")
	if service == nil {
		t.Fatal("DocumentService descriptor is missing")
	}
	method := service.Methods().ByName("GetDocument")
	if method == nil {
		t.Fatal("GetDocument descriptor is missing")
	}

	methodThrows := proto.GetExtension(method.Options(), E_Throws).([]FailureReason)
	if len(methodThrows) != 1 || methodThrows[0] != FailureReason_FAILURE_REASON_NOT_FOUND {
		t.Fatalf("method throws = %v, want [FAILURE_REASON_NOT_FOUND]", methodThrows)
	}

	serviceThrows := proto.GetExtension(service.Options(), E_ServiceThrows).([]FailureReason)
	if len(serviceThrows) != 1 || serviceThrows[0] != FailureReason_FAILURE_REASON_DENIED {
		t.Fatalf("service throws = %v, want [FAILURE_REASON_DENIED]", serviceThrows)
	}

	for _, extension := range []struct {
		name string
		desc protoreflect.ExtensionType
	}{
		{"throws", E_Throws},
		{"service_throws", E_ServiceThrows},
	} {
		fd := extension.desc.TypeDescriptor().Descriptor()
		options, ok := fd.Options().(*descriptorpb.FieldOptions)
		if !ok || options == nil {
			t.Fatalf("extension %s has no FieldOptions", extension.name)
		}
		if got := proto.GetExtension(options, errors.E_Throws).(bool); !got {
			t.Fatalf("extension %s: (sylphy.errors.v1.throws) = %v, want true", extension.name, got)
		}
	}
}
