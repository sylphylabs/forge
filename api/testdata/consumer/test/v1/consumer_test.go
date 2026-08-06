package testv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/openkratos/api/errors/v1"
)

func TestErrorAnnotations(t *testing.T) {
	descriptor := FailureReason(0).Descriptor()
	value := descriptor.Values().ByName("FAILURE_REASON_NOT_FOUND")
	if value == nil {
		t.Fatal("FAILURE_REASON_NOT_FOUND descriptor is missing")
	}
	if got := proto.GetExtension(value.Options(), errors.E_Code); got != int32(404) {
		t.Fatalf("error code = %v, want 404", got)
	}
	if got := proto.GetExtension(descriptor.Options(), errors.E_DefaultCode); got != int32(500) {
		t.Fatalf("default error code = %v, want 500", got)
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
