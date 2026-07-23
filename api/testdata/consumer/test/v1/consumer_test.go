package testv1

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	errorsv1 "github.com/openkratos/api/errors/v1"
	policyv1 "github.com/openkratos/api/policy/v1"
)

func TestErrorAnnotations(t *testing.T) {
	descriptor := FailureReason(0).Descriptor()
	value := descriptor.Values().ByName("FAILURE_REASON_NOT_FOUND")
	if value == nil {
		t.Fatal("FAILURE_REASON_NOT_FOUND descriptor is missing")
	}
	if got := proto.GetExtension(value.Options(), errorsv1.E_Code); got != int32(404) {
		t.Fatalf("error code = %v, want 404", got)
	}
	if got := proto.GetExtension(descriptor.Options(), errorsv1.E_DefaultCode); got != int32(500) {
		t.Fatalf("default error code = %v, want 500", got)
	}
}

func TestOperationPolicyAnnotations(t *testing.T) {
	service := File_test_v1_consumer_proto.Services().ByName("DocumentService")
	if service == nil {
		t.Fatal("DocumentService descriptor is missing")
	}
	servicePolicy, ok := proto.GetExtension(service.Options(), policyv1.E_DefaultPolicy).(*policyv1.OperationPolicy)
	if !ok || servicePolicy.GetAccess() != policyv1.Access_ACCESS_AUTHENTICATED {
		t.Fatalf("service policy = %v", servicePolicy)
	}

	method := service.Methods().ByName("GetDocument")
	if method == nil {
		t.Fatal("GetDocument descriptor is missing")
	}
	methodOptions := method.Options().(*descriptorpb.MethodOptions)
	if got := methodOptions.GetIdempotencyLevel(); got != descriptorpb.MethodOptions_IDEMPOTENT {
		t.Fatalf("idempotency level = %v", got)
	}
	methodPolicy, ok := proto.GetExtension(methodOptions, policyv1.E_Policy).(*policyv1.OperationPolicy)
	if !ok {
		t.Fatalf("method policy type = %T", proto.GetExtension(methodOptions, policyv1.E_Policy))
	}
	if methodPolicy.GetAccess() != policyv1.Access_ACCESS_AUTHORIZED {
		t.Fatalf("method access = %v", methodPolicy.GetAccess())
	}
	if methodPolicy.GetIdempotencyClass() != "request-key" {
		t.Fatalf("idempotency class = %q", methodPolicy.GetIdempotencyClass())
	}
	if got := methodPolicy.GetPermissions().GetRequireAll(); len(got) != 1 || got[0] != "documents.read" {
		t.Fatalf("required permissions = %v", got)
	}
}
