package operationpolicy

import (
	"strings"
	"testing"

	"github.com/openkratos/api/policy/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

func TestResolveMerge(t *testing.T) {
	method := testMethod(t, &policy.OperationPolicy{
		Access:           policy.Access_ACCESS_AUTHENTICATED.Enum(),
		ValidateRequest:  proto.Bool(true),
		Audit:            proto.Bool(true),
		IdempotencyClass: proto.String("request-key"),
		RateClass:        proto.String("read"),
		BudgetClass:      proto.String("interactive"),
	}, &policy.OperationPolicy{
		Access: policy.Access_ACCESS_AUTHORIZED.Enum(),
		Permissions: &policy.PermissionPolicy{
			RequireAll: []string{"documents.read"},
			RequireAny: []string{"documents.owner", "documents.admin"},
		},
		ValidateRequest: proto.Bool(false),
		RateClass:       proto.String(""),
	}, descriptorpb.MethodOptions_IDEMPOTENT, false, false)

	got, err := Resolve(method)
	if err != nil {
		t.Fatal(err)
	}
	if got.Access != policy.Access_ACCESS_AUTHORIZED {
		t.Errorf("Access = %v", got.Access)
	}
	if got.ValidateRequest {
		t.Error("ValidateRequest = true, want explicit method false")
	}
	if !got.Audit {
		t.Error("Audit = false, want inherited true")
	}
	if got.IdempotencyClass != "request-key" || got.RateClass != "" || got.BudgetClass != "interactive" {
		t.Errorf("classes = idempotency:%q rate:%q budget:%q", got.IdempotencyClass, got.RateClass, got.BudgetClass)
	}
	if got.IdempotencyLevel != descriptorpb.MethodOptions_IDEMPOTENT {
		t.Errorf("IdempotencyLevel = %v", got.IdempotencyLevel)
	}
	if gotAll := got.RequireAll(); len(gotAll) != 1 || gotAll[0] != "documents.read" {
		t.Errorf("RequireAll = %v", gotAll)
	}
	if gotAny := got.RequireAny(); len(gotAny) != 2 || gotAny[0] != "documents.owner" || gotAny[1] != "documents.admin" {
		t.Errorf("RequireAny = %v", gotAny)
	}

	all := got.RequireAll()
	all[0] = "mutated"
	if got.RequireAll()[0] != "documents.read" {
		t.Fatal("RequireAll exposes mutable policy storage")
	}
}

func TestResolveRejectsInvalidPolicy(t *testing.T) {
	tests := []struct {
		name             string
		service          *policy.OperationPolicy
		method           *policy.OperationPolicy
		idempotencyLevel descriptorpb.MethodOptions_IdempotencyLevel
		want             string
	}{
		{name: "missing access", want: "access must be explicit"},
		{
			name:   "public permissions",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_PUBLIC.Enum(), Permissions: &policy.PermissionPolicy{RequireAll: []string{"documents.read"}}},
			want:   "ACCESS_PUBLIC access cannot require permissions",
		},
		{
			name:   "authorized without permissions",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_AUTHORIZED.Enum()},
			want:   "authorized access requires at least one permission",
		},
		{
			name:   "duplicate permission",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_AUTHORIZED.Enum(), Permissions: &policy.PermissionPolicy{RequireAll: []string{"documents.read", "documents.read"}}},
			want:   `permission "documents.read" appears in require_all and require_all`,
		},
		{
			name:   "permission in both lists",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_AUTHORIZED.Enum(), Permissions: &policy.PermissionPolicy{RequireAll: []string{"documents.read"}, RequireAny: []string{"documents.read"}}},
			want:   `permission "documents.read" appears in require_all and require_any`,
		},
		{
			name:   "invalid permission",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_AUTHORIZED.Enum(), Permissions: &policy.PermissionPolicy{RequireAll: []string{" Documents.Read "}}},
			want:   "invalid require_all permission",
		},
		{
			name:   "invalid class",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_PUBLIC.Enum(), RateClass: proto.String("Read Fast")},
			want:   "invalid rate_class",
		},
		{
			name:   "idempotency semantics missing",
			method: &policy.OperationPolicy{Access: policy.Access_ACCESS_PUBLIC.Enum(), IdempotencyClass: proto.String("request-key")},
			want:   "idempotency_class requires idempotency_level IDEMPOTENT",
		},
		{
			name:             "permission clear makes authorized invalid",
			service:          &policy.OperationPolicy{Access: policy.Access_ACCESS_AUTHORIZED.Enum(), Permissions: &policy.PermissionPolicy{RequireAll: []string{"documents.read"}}},
			method:           &policy.OperationPolicy{Permissions: new(policy.PermissionPolicy)},
			idempotencyLevel: descriptorpb.MethodOptions_IDEMPOTENT,
			want:             "authorized access requires at least one permission",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			method := testMethod(t, test.service, test.method, test.idempotencyLevel, false, false)
			_, err := Resolve(method)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want containing %q", err, test.want)
			}
			if !strings.Contains(err.Error(), "/test.v1.DocumentService/GetDocument") {
				t.Fatalf("Resolve() error has no operation: %v", err)
			}
		})
	}
}

func TestResolveRejectsStreamingPolicy(t *testing.T) {
	tests := []struct {
		name         string
		clientStream bool
		serverStream bool
		want         string
	}{
		{name: "client", clientStream: true, want: "client streaming"},
		{name: "server", serverStream: true, want: "server streaming"},
		{name: "bidirectional", clientStream: true, serverStream: true, want: "bidirectional streaming"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			method := testMethod(t,
				&policy.OperationPolicy{Access: policy.Access_ACCESS_PUBLIC.Enum()},
				nil,
				descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN,
				test.clientStream,
				test.serverStream,
			)
			_, err := Resolve(method)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Resolve() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func testMethod(
	t *testing.T,
	servicePolicy *policy.OperationPolicy,
	methodPolicy *policy.OperationPolicy,
	idempotencyLevel descriptorpb.MethodOptions_IdempotencyLevel,
	clientStreaming bool,
	serverStreaming bool,
) protoreflect.MethodDescriptor {
	t.Helper()
	serviceOptions := new(descriptorpb.ServiceOptions)
	if servicePolicy != nil {
		proto.SetExtension(serviceOptions, policy.E_DefaultPolicy, servicePolicy)
	}
	methodOptions := &descriptorpb.MethodOptions{IdempotencyLevel: idempotencyLevel.Enum()}
	if methodPolicy != nil {
		proto.SetExtension(methodOptions, policy.E_Policy, methodPolicy)
	}
	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/document.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"openkratos/policy/v1/policy.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("GetDocumentRequest")},
			{Name: proto.String("GetDocumentResponse")},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:    proto.String("DocumentService"),
			Options: serviceOptions,
			Method: []*descriptorpb.MethodDescriptorProto{{
				Name:            proto.String("GetDocument"),
				InputType:       proto.String(".test.v1.GetDocumentRequest"),
				OutputType:      proto.String(".test.v1.GetDocumentResponse"),
				Options:         methodOptions,
				ClientStreaming: proto.Bool(clientStreaming),
				ServerStreaming: proto.Bool(serverStreaming),
			}},
		}},
	}
	descriptor, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		t.Fatal(err)
	}
	return descriptor.Services().Get(0).Methods().Get(0)
}
