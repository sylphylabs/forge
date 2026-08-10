package testv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
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
