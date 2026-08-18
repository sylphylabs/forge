package main

import (
	"strings"
	"testing"

	highv3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"

	errorapi "github.com/sylphylabs/forge/api/errors/v1"
	"github.com/sylphylabs/forge/cmd/internal/generator"
	openapigen "github.com/sylphylabs/forge/cmd/internal/openapi/generator"
)

// The extension numbers the throws fixtures use. The marker number is the one
// sylphy/errors/v1/errors.proto declares; the application numbers are
// arbitrary picks from the internal-use extension range.
const (
	throwsMarkerNumber    = 500103
	methodThrowsNumber    = 50000
	serviceThrowsNumber   = 50001
	bufValidateFieldRules = 1159
)

func TestGenerateOpenAPIThrowsProducesExactErrorResponses(t *testing.T) {
	content, document := generateThrowsDocument(t, testConfig(), throwsServiceFile(nil, nil))

	operation := findOperation(t, document, "/v1/books/{name}", "GET")

	forbidden := findOperationResponse(t, operation, "403")
	wantForbidden := "PERMISSION_DENIED (test.v1) — reasons: FAILURE_REASON_DENIED, FAILURE_REASON_EXPIRED"
	if forbidden.Description != wantForbidden {
		t.Fatalf("403 description = %q, want %q", forbidden.Description, wantForbidden)
	}
	assertProblemContent(t, forbidden)

	notFound := findOperationResponse(t, operation, "404")
	if want := "NOT_FOUND (test.v1) — reasons: FAILURE_REASON_NOT_FOUND"; notFound.Description != want {
		t.Fatalf("404 description = %q, want %q", notFound.Description, want)
	}

	internal := findOperationResponse(t, operation, "500")
	if want := "INTERNAL (test.v1) — reasons: FAILURE_REASON_STALE"; internal.Description != want {
		t.Fatalf("500 description = %q, want %q", internal.Description, want)
	}

	// The service-level declaration alone covers the method with no method
	// declaration of its own.
	list := findOperation(t, document, "/v1/books", "GET")
	listForbidden := findOperationResponse(t, list, "403")
	if want := "PERMISSION_DENIED (test.v1) — reasons: FAILURE_REASON_DENIED"; listForbidden.Description != want {
		t.Fatalf("service-level 403 description = %q, want %q", listForbidden.Description, want)
	}
	if findOptionalResponse(list, "404") != nil {
		t.Fatal("method-level declaration leaked onto a method that did not declare it")
	}

	// The default response coexists with the exact responses.
	if operation.Responses.Default == nil {
		t.Fatal("default response was dropped from an operation with throws declarations")
	}

	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIValidationConstraintsAddBadRequest(t *testing.T) {
	content, document := generateThrowsDocument(t, testConfig(), validationServiceFile())

	// A method with throws declarations and a constrained request merges the
	// framework identity into the 400 alongside the declared reason.
	operation := findOperation(t, document, "/v1/books/{name}", "GET")
	badRequest := findOperationResponse(t, operation, "400")
	want := "INVALID_ARGUMENT (forge.sylphylabs.io) — reasons: VALIDATION_FAILED\n" +
		"INVALID_ARGUMENT (test.v1) — reasons: FAILURE_REASON_NOT_FOUND"
	if badRequest.Description != want {
		t.Fatalf("400 description = %q, want %q", badRequest.Description, want)
	}
	assertProblemContent(t, badRequest)

	// A method with no throws declaration but a transitively constrained
	// request still documents the 400.
	nested := findOperation(t, document, "/v1/books", "POST")
	nestedBadRequest := findOperationResponse(t, nested, "400")
	if want := "INVALID_ARGUMENT (forge.sylphylabs.io) — reasons: VALIDATION_FAILED"; nestedBadRequest.Description != want {
		t.Fatalf("validation-only 400 description = %q, want %q", nestedBadRequest.Description, want)
	}

	// An unconstrained request produces no 400.
	clean := findOperation(t, document, "/v1/shelves", "GET")
	if findOptionalResponse(clean, "400") != nil {
		t.Fatal("unconstrained method received a validation 400")
	}

	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIValidationReasonDisabled(t *testing.T) {
	conf := testConfig()
	validationReason := false
	conf.ValidationReason = &validationReason
	_, document := generateThrowsDocument(t, conf, validationServiceFile())

	nested := findOperation(t, document, "/v1/books", "POST")
	if findOptionalResponse(nested, "400") != nil {
		t.Fatal("validation_reason=false still documented a validation 400")
	}

	// Declared throws keep working with the option off.
	operation := findOperation(t, document, "/v1/books/{name}", "GET")
	badRequest := findOperationResponse(t, operation, "400")
	if want := "INVALID_ARGUMENT (test.v1) — reasons: FAILURE_REASON_NOT_FOUND"; badRequest.Description != want {
		t.Fatalf("400 description = %q, want %q", badRequest.Description, want)
	}
}

func TestGenerateOpenAPIThrowsMarkerOnIllegalHostFails(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*descriptorpb.FileDescriptorProto)
		wantErr string
	}{
		{
			name: "marker on a plain message field",
			mutate: func(file *descriptorpb.FileDescriptorProto) {
				field := file.MessageType[0].Field[0]
				field.Options = markedFieldOptions()
			},
			wantErr: "is not an extension field",
		},
		{
			name: "marker on an extension of the wrong options message",
			mutate: func(file *descriptorpb.FileDescriptorProto) {
				file.Extension = append(file.Extension, &descriptorpb.FieldDescriptorProto{
					Name:     proto.String("file_throws"),
					Number:   proto.Int32(50002),
					Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
					Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
					TypeName: proto.String(".test.v1.FailureReason"),
					Extendee: proto.String(".google.protobuf.FileOptions"),
					Options:  markedFieldOptions(),
				})
			},
			wantErr: "extends google.protobuf.FileOptions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := throwsServiceFile(nil, nil)
			tt.mutate(file)
			assertThrowsGenerationFails(t, testConfig(), file, tt.wantErr)
		})
	}
}

func TestGenerateOpenAPIThrowsMarkedFieldMustBeRepeatedEnum(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*descriptorpb.FieldDescriptorProto)
	}{
		{
			name: "string-typed extension",
			mutate: func(extension *descriptorpb.FieldDescriptorProto) {
				extension.Type = descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum()
				extension.TypeName = nil
			},
		},
		{
			name: "non-repeated enum extension",
			mutate: func(extension *descriptorpb.FieldDescriptorProto) {
				extension.Label = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := throwsServiceFile(nil, nil)
			tt.mutate(file.Extension[0])
			// Strip the now type-mismatched declaration payloads so the failure
			// under test is the field shape, not a wire decoding artifact.
			for _, service := range file.Service {
				for _, method := range service.Method {
					method.Options.ProtoReflect().SetUnknown(nil)
				}
			}
			assertThrowsGenerationFails(t, testConfig(), file, "is not a repeated enum")
		})
	}
}

func TestGenerateOpenAPIThrowsRejectsZeroValue(t *testing.T) {
	file := throwsServiceFile(func(method *descriptorpb.MethodDescriptorProto) {
		appendUnknownField(method.Options, rawVarintField(methodThrowsNumber, 0))
	}, nil)
	assertThrowsGenerationFails(t, testConfig(), file, "must not reference the zero value")
}

func TestGenerateOpenAPIThrowsRejectsValueWithoutKind(t *testing.T) {
	file := throwsServiceFile(nil, func(file *descriptorpb.FileDescriptorProto) {
		file.EnumType = append(file.EnumType, &descriptorpb.EnumDescriptorProto{
			Name: proto.String("BareReason"),
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("BARE_REASON_UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("BARE_REASON_LOST"), Number: proto.Int32(1)},
			},
		})
		file.Extension = append(file.Extension, &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("bare_throws"),
			Number:   proto.Int32(50003),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
			Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
			TypeName: proto.String(".test.v1.BareReason"),
			Extendee: proto.String(".google.protobuf.MethodOptions"),
			Options:  markedFieldOptions(),
		})
		appendUnknownField(file.Service[0].Method[0].Options, rawVarintField(50003, 1))
	})
	assertThrowsGenerationFails(t, testConfig(), file, "resolves to no kind")
}

func TestGenerateOpenAPIThrowsRejectsDuplicateDeclaration(t *testing.T) {
	// FAILURE_REASON_DENIED is already declared at the service level.
	file := throwsServiceFile(func(method *descriptorpb.MethodDescriptorProto) {
		appendUnknownField(method.Options, rawVarintField(methodThrowsNumber, 2))
	}, nil)
	assertThrowsGenerationFails(t, testConfig(), file, "declared more than once")
}

func TestGenerateOpenAPIThrowsIgnoresUnresolvableExtensions(t *testing.T) {
	// An extension no descriptor in the request declares stays unknown. It must
	// not fail generation, and the resolvable declarations next to it must all
	// still be discovered.
	file := throwsServiceFile(func(method *descriptorpb.MethodDescriptorProto) {
		appendUnknownField(method.Options, rawVarintField(60000, 1))
	}, nil)
	_, document := generateThrowsDocument(t, testConfig(), file)

	operation := findOperation(t, document, "/v1/books/{name}", "GET")
	findOperationResponse(t, operation, "404")
	findOperationResponse(t, operation, "403")
	findOperationResponse(t, operation, "500")
}

// assertProblemContent asserts a response carries the shared Forge problem
// schema under the problem media type.
func assertProblemContent(t *testing.T, response *highv3.Response) {
	t.Helper()

	if orderedmap.Len(response.Content) != 1 {
		t.Fatalf("error response media types = %d, want 1", orderedmap.Len(response.Content))
	}
	pair := response.Content.First()
	if pair.Key() != "application/problem+json" {
		t.Fatalf("error response media type = %q, want application/problem+json", pair.Key())
	}
	schema := pair.Value().Schema
	if schema == nil || !schema.IsReference() {
		t.Fatal("error response schema is not a reference")
	}
	if want := "#/components/schemas/" + openapigen.DefaultErrorSchemaName; schema.GetReference() != want {
		t.Fatalf("error response schema reference = %q, want %q", schema.GetReference(), want)
	}
}

func generateThrowsDocument(t *testing.T, conf openapigen.Configuration, file *descriptorpb.FileDescriptorProto, dependencies ...*descriptorpb.FileDescriptorProto) (string, *highv3.Document) {
	t.Helper()
	dependencies = append([]*descriptorpb.FileDescriptorProto{errorsProtoWithThrows(), bufValidateFile()}, dependencies...)
	plugin := newOpenAPIPluginForFile(t, file, dependencies...)
	generator.Configure(plugin)
	if err := generateOpenAPI(plugin, conf); err != nil {
		t.Fatalf("generateOpenAPI() error = %v", err)
	}
	response := plugin.Response()
	if response.GetError() != "" {
		t.Fatalf("generation error = %s", response.GetError())
	}
	content := response.File[0].GetContent()
	return content, parseDocument(t, content)
}

func assertThrowsGenerationFails(t *testing.T, conf openapigen.Configuration, file *descriptorpb.FileDescriptorProto, wantErr string) {
	t.Helper()
	plugin := newOpenAPIPluginForFile(t, file, errorsProtoWithThrows(), bufValidateFile())
	generator.Configure(plugin)
	err := generateOpenAPI(plugin, conf)
	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("generateOpenAPI() error = %v, want containing %q", err, wantErr)
	}
}

// errorsProtoWithThrows is the sylphy/errors/v1/errors.proto descriptor with
// the throws marker extension. The published API module the plugin compiles
// against predates the marker, so the fixture appends the field the current
// proto source declares; production requests carry it because the
// application's buf build includes the current errors.proto.
func errorsProtoWithThrows() *descriptorpb.FileDescriptorProto {
	file := protodesc.ToFileDescriptorProto(errorapi.File_sylphy_errors_v1_errors_proto)
	file.Extension = append(file.Extension, &descriptorpb.FieldDescriptorProto{
		Name:     proto.String("throws"),
		Number:   proto.Int32(throwsMarkerNumber),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum(),
		Extendee: proto.String(".google.protobuf.FieldOptions"),
	})
	return file
}

// bufValidateFile declares the minimal shape of buf.validate the fixtures
// need: a FieldOptions extension under the buf.validate package, which is
// what constraint discovery matches on.
func bufValidateFile() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("buf/validate/validate.proto"),
		Package:    proto.String("buf.validate"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/buf/validate;validate"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("FieldRules"),
				Field: []*descriptorpb.FieldDescriptorProto{
					scalarField("required", 25, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
				},
			},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("field"),
				Number:   proto.Int32(bufValidateFieldRules),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".buf.validate.FieldRules"),
				Extendee: proto.String(".google.protobuf.FieldOptions"),
			},
		},
	}
}

// throwsServiceFile builds the primary throws fixture:
//
//	enum FailureReason {
//	  option (sylphy.errors.v1.default_kind) = KIND_INTERNAL;
//	  FAILURE_REASON_UNSPECIFIED = 0;
//	  FAILURE_REASON_NOT_FOUND = 1 [(sylphy.errors.v1.kind) = KIND_NOT_FOUND];
//	  FAILURE_REASON_DENIED = 2 [(sylphy.errors.v1.kind) = KIND_PERMISSION_DENIED];
//	  FAILURE_REASON_EXPIRED = 3 [(sylphy.errors.v1.kind) = KIND_PERMISSION_DENIED];
//	  FAILURE_REASON_STALE = 4; // default_kind applies
//	}
//	extend google.protobuf.MethodOptions {
//	  repeated FailureReason throws = 50000 [(sylphy.errors.v1.throws) = true];
//	}
//	extend google.protobuf.ServiceOptions {
//	  repeated FailureReason service_throws = 50001 [(sylphy.errors.v1.throws) = true];
//	}
//	service Library {
//	  option (service_throws) = FAILURE_REASON_DENIED;
//	  rpc GetBook  — throws NOT_FOUND, EXPIRED, STALE — GET /v1/books/{name}
//	  rpc ListBooks — no method declaration — GET /v1/books
//	}
func throwsServiceFile(mutateGetBook func(*descriptorpb.MethodDescriptorProto), mutateFile func(*descriptorpb.FileDescriptorProto)) *descriptorpb.FileDescriptorProto {
	getBookOptions := httpRuleOptions(&annotations.HttpRule{
		Pattern: &annotations.HttpRule_Get{Get: "/v1/books/{name}"},
	})
	appendUnknownField(getBookOptions, rawVarintField(methodThrowsNumber, 1, 3, 4))

	serviceOptions := new(descriptorpb.ServiceOptions)
	appendUnknownField(serviceOptions, rawVarintField(serviceThrowsNumber, 2))

	getBook := &descriptorpb.MethodDescriptorProto{
		Name:       proto.String("GetBook"),
		InputType:  proto.String(".test.v1.GetBookRequest"),
		OutputType: proto.String(".test.v1.Book"),
		Options:    getBookOptions,
	}
	if mutateGetBook != nil {
		mutateGetBook(getBook)
	}

	file := &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/library.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"google/api/annotations.proto",
			"google/protobuf/descriptor.proto",
			"sylphy/errors/v1/errors.proto",
		},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{failureReasonEnum()},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:  proto.String("GetBookRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("name", 1)},
			},
			{
				Name:  proto.String("ListBooksRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("parent", 1)},
			},
			{
				Name:  proto.String("Book"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("name", 1)},
			},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("throws"),
				Number:   proto.Int32(methodThrowsNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: proto.String(".test.v1.FailureReason"),
				Extendee: proto.String(".google.protobuf.MethodOptions"),
				Options:  markedFieldOptions(),
			},
			{
				Name:     proto.String("service_throws"),
				Number:   proto.Int32(serviceThrowsNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: proto.String(".test.v1.FailureReason"),
				Extendee: proto.String(".google.protobuf.ServiceOptions"),
				Options:  markedFieldOptions(),
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name:    proto.String("Library"),
				Options: serviceOptions,
				Method: []*descriptorpb.MethodDescriptorProto{
					getBook,
					{
						Name:       proto.String("ListBooks"),
						InputType:  proto.String(".test.v1.ListBooksRequest"),
						OutputType: proto.String(".test.v1.Book"),
						Options: httpRuleOptions(&annotations.HttpRule{
							Pattern: &annotations.HttpRule_Get{Get: "/v1/books"},
						}),
					},
				},
			},
		},
	}
	if mutateFile != nil {
		mutateFile(file)
	}
	return file
}

// validationServiceFile builds the validation fixture: GetBook declares one
// throws reason whose kind projects to 400 and has a directly constrained
// request; ListBooks declares nothing but reaches a constraint through a
// nested (and self-recursive) message; ListShelves has no constraints at all.
func validationServiceFile() *descriptorpb.FileDescriptorProto {
	constrainedName := stringField("name", 1)
	constrainedName.Options = new(descriptorpb.FieldOptions)
	appendUnknownField(constrainedName.Options, rawBytesField(bufValidateFieldRules, rawVarintField(25, 1)))

	getBookOptions := httpRuleOptions(&annotations.HttpRule{
		Pattern: &annotations.HttpRule_Get{Get: "/v1/books/{name}"},
	})
	appendUnknownField(getBookOptions, rawVarintField(methodThrowsNumber, 1))

	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/validation.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"google/api/annotations.proto",
			"google/protobuf/descriptor.proto",
			"sylphy/errors/v1/errors.proto",
			"buf/validate/validate.proto",
		},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{invalidReasonEnum()},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:  proto.String("GetBookRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{constrainedName},
			},
			{
				Name: proto.String("ListBooksRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("filter", 1, ".test.v1.Filter"),
				},
			},
			{
				Name: proto.String("Filter"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("and", 1, ".test.v1.Filter"),
					constrainedField("term", 2),
				},
			},
			{
				Name:  proto.String("ListShelvesRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("parent", 1)},
			},
			{
				Name:  proto.String("Book"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("name", 1)},
			},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("throws"),
				Number:   proto.Int32(methodThrowsNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum(),
				TypeName: proto.String(".test.v1.FailureReason"),
				Extendee: proto.String(".google.protobuf.MethodOptions"),
				Options:  markedFieldOptions(),
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Library"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("GetBook"),
						InputType:  proto.String(".test.v1.GetBookRequest"),
						OutputType: proto.String(".test.v1.Book"),
						Options:    getBookOptions,
					},
					{
						Name:       proto.String("ListBooks"),
						InputType:  proto.String(".test.v1.ListBooksRequest"),
						OutputType: proto.String(".test.v1.Book"),
						// POST with a whole-message body: a self-recursive message
						// is not a valid query-parameter shape, and the recursion
						// under test is constraint discovery, not query binding.
						Options: httpRuleOptions(&annotations.HttpRule{
							Pattern: &annotations.HttpRule_Post{Post: "/v1/books"},
							Body:    "*",
						}),
					},
					{
						Name:       proto.String("ListShelves"),
						InputType:  proto.String(".test.v1.ListShelvesRequest"),
						OutputType: proto.String(".test.v1.Book"),
						Options: httpRuleOptions(&annotations.HttpRule{
							Pattern: &annotations.HttpRule_Get{Get: "/v1/shelves"},
						}),
					},
				},
			},
		},
	}
}

func failureReasonEnum() *descriptorpb.EnumDescriptorProto {
	enumOptions := new(descriptorpb.EnumOptions)
	proto.SetExtension(enumOptions, errorapi.E_DefaultKind, errorapi.Kind_KIND_INTERNAL)
	return &descriptorpb.EnumDescriptorProto{
		Name:    proto.String("FailureReason"),
		Options: enumOptions,
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: proto.String("FAILURE_REASON_UNSPECIFIED"), Number: proto.Int32(0)},
			enumValueWithKind("FAILURE_REASON_NOT_FOUND", 1, errorapi.Kind_KIND_NOT_FOUND),
			enumValueWithKind("FAILURE_REASON_DENIED", 2, errorapi.Kind_KIND_PERMISSION_DENIED),
			enumValueWithKind("FAILURE_REASON_EXPIRED", 3, errorapi.Kind_KIND_PERMISSION_DENIED),
			{Name: proto.String("FAILURE_REASON_STALE"), Number: proto.Int32(4)},
		},
	}
}

// invalidReasonEnum declares one reason whose kind projects to 400, so a
// declared reason and the framework validation identity share a status code.
func invalidReasonEnum() *descriptorpb.EnumDescriptorProto {
	return &descriptorpb.EnumDescriptorProto{
		Name: proto.String("FailureReason"),
		Value: []*descriptorpb.EnumValueDescriptorProto{
			{Name: proto.String("FAILURE_REASON_UNSPECIFIED"), Number: proto.Int32(0)},
			enumValueWithKind("FAILURE_REASON_NOT_FOUND", 1, errorapi.Kind_KIND_INVALID_ARGUMENT),
		},
	}
}

func enumValueWithKind(name string, number int32, kind errorapi.Kind) *descriptorpb.EnumValueDescriptorProto {
	options := new(descriptorpb.EnumValueOptions)
	proto.SetExtension(options, errorapi.E_Kind, kind)
	return &descriptorpb.EnumValueDescriptorProto{
		Name:    proto.String(name),
		Number:  proto.Int32(number),
		Options: options,
	}
}

func constrainedField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	field := stringField(name, number)
	field.Options = new(descriptorpb.FieldOptions)
	appendUnknownField(field.Options, rawBytesField(bufValidateFieldRules, rawVarintField(25, 1)))
	return field
}

// markedFieldOptions builds FieldOptions carrying
// (sylphy.errors.v1.throws) = true as an unknown field, exactly the form a
// production CodeGeneratorRequest delivers to a plugin that does not link the
// extension's generated type.
func markedFieldOptions() *descriptorpb.FieldOptions {
	options := new(descriptorpb.FieldOptions)
	appendUnknownField(options, rawVarintField(throwsMarkerNumber, 1))
	return options
}

func appendUnknownField(options proto.Message, raw []byte) {
	reflection := options.ProtoReflect()
	reflection.SetUnknown(append(reflection.GetUnknown(), raw...))
}

func rawVarintField(number protowire.Number, values ...uint64) []byte {
	var raw []byte
	for _, value := range values {
		raw = protowire.AppendTag(raw, number, protowire.VarintType)
		raw = protowire.AppendVarint(raw, value)
	}
	return raw
}

func rawBytesField(number protowire.Number, payload []byte) []byte {
	raw := protowire.AppendTag(nil, number, protowire.BytesType)
	return protowire.AppendBytes(raw, payload)
}
