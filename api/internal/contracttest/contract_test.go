package contracttest

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sylphylabs/forge/api/errors/v1"
	message "github.com/sylphylabs/forge/api/message/v1"
	openapi "github.com/sylphylabs/forge/api/openapi/v1"
)

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

func TestOpenAPIAnnotationOptions(t *testing.T) {
	fileOptions := new(descriptorpb.FileOptions)
	proto.SetExtension(fileOptions, openapi.E_Document, &openapi.Document{
		Title:   "Library API",
		Version: "1.2.3",
		Servers: []*openapi.Server{{Url: "https://api.example.com"}},
		SecuritySchemes: []*openapi.SecurityScheme{{
			Name:   "bearer",
			Scheme: &openapi.SecurityScheme_HttpBearer{HttpBearer: &openapi.HTTPBearer{BearerFormat: "JWT"}},
		}},
	})
	document, ok := proto.GetExtension(fileOptions, openapi.E_Document).(*openapi.Document)
	if !ok || document.GetTitle() != "Library API" || len(document.GetServers()) != 1 {
		t.Fatalf("document annotation round-trip = %v", document)
	}

	methodOptions := new(descriptorpb.MethodOptions)
	proto.SetExtension(methodOptions, openapi.E_Operation, &openapi.Operation{
		Summary:  "Get one book",
		Tags:     []string{"books"},
		Security: []*openapi.SecurityRequirement{{Schemes: []string{"bearer"}}},
	})
	operation, ok := proto.GetExtension(methodOptions, openapi.E_Operation).(*openapi.Operation)
	if !ok || operation.GetSummary() != "Get one book" || len(operation.GetSecurity()) != 1 {
		t.Fatalf("operation annotation round-trip = %v", operation)
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
		{"google.protobuf.FileOptions", 500301, "sylphy.openapi.v1.document"},
		{"google.protobuf.MethodOptions", 500302, "sylphy.openapi.v1.operation"},
		{"google.protobuf.MessageOptions", 500303, "sylphy.openapi.v1.schema"},
		{"google.protobuf.FieldOptions", 500304, "sylphy.openapi.v1.field"},
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
