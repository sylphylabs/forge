package main

// This file proves the sylphy.openapi.v1 annotation vocabulary end to end:
// proto descriptors carrying document, operation, schema, and field
// annotations travel through protoc-gen-openapi and surface in the generated
// document. The published API module the plugin compiles against predates the
// vocabulary, so the fixture declares the annotations file as a descriptor and
// encodes the annotation payloads as unknown wire fields — exactly the form a
// production CodeGeneratorRequest delivers to a plugin that does not link the
// generated extension types.

import (
	"strings"
	"testing"

	highv3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sylphylabs/forge/cmd/internal/generator"
)

// The sylphy.openapi.v1 extension numbers, as declared in
// api/proto/sylphy/openapi/v1/annotations.proto.
const (
	openapiDocumentNumber  = 500301
	openapiOperationNumber = 500302
	openapiSchemaNumber    = 500303
	openapiFieldNumber     = 500304
)

func TestGenerateOpenAPIAppliesSylphyAnnotations(t *testing.T) {
	content, document := generateAnnotatedDocument(t, annotatedServiceFile())

	// Document annotation: info, servers, and security schemes.
	if document.Info.Title != "Document API" {
		t.Fatalf("info.title = %q, want %q", document.Info.Title, "Document API")
	}
	if document.Info.Version != "9.9.9" {
		t.Fatalf("info.version = %q, want %q", document.Info.Version, "9.9.9")
	}
	if document.Info.Description != "Manages documents." {
		t.Fatalf("info.description = %q", document.Info.Description)
	}
	if len(document.Servers) == 0 || document.Servers[0].URL != "https://api.example.com" {
		t.Fatalf("servers = %v, want the annotated server first", document.Servers)
	}
	bearer := document.Components.SecuritySchemes.GetOrZero("bearer")
	if bearer == nil || bearer.Type != "http" || bearer.Scheme != "bearer" || bearer.BearerFormat != "JWT" {
		t.Fatalf("bearer scheme = %+v, want http bearer JWT", bearer)
	}
	apiKey := document.Components.SecuritySchemes.GetOrZero("api_key")
	if apiKey == nil || apiKey.Type != "apiKey" || apiKey.In != "header" || apiKey.Name != "X-Api-Key" {
		t.Fatalf("api key scheme = %+v, want apiKey in header X-Api-Key", apiKey)
	}

	// Operation annotation: summary, tags, deprecation, security requirement.
	operation := findOperation(t, document, "/v1/documents", "POST")
	if operation.Summary != "Get one document" {
		t.Fatalf("operation summary = %q", operation.Summary)
	}
	if len(operation.Tags) != 1 || operation.Tags[0] != "documents" {
		t.Fatalf("operation tags = %v, want [documents]", operation.Tags)
	}
	if operation.Deprecated == nil || !*operation.Deprecated {
		t.Fatal("operation is not marked deprecated")
	}
	if len(operation.Security) != 1 {
		t.Fatalf("operation security requirements = %d, want 1", len(operation.Security))
	}
	if scopes, ok := operation.Security[0].Requirements.Get("bearer"); !ok || len(scopes) != 0 {
		t.Fatalf("operation security = %v, want bearer with no scopes", operation.Security[0].Requirements)
	}

	// Schema and field annotations: descriptions and example.
	schema := findComponentSchema(t, document, "test.v1.GetDocumentRequest")
	if schema.Description != "Request to fetch one document." {
		t.Fatalf("schema description = %q", schema.Description)
	}
	nameProperty := findProperty(t, schema, "name")
	if nameProperty.Description != "Resource name of the document." {
		t.Fatalf("property description = %q", nameProperty.Description)
	}
	if nameProperty.Example == nil || nameProperty.Example.Value != "documents/1" {
		t.Fatalf("property example = %v, want documents/1", nameProperty.Example)
	}

	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIRejectsDanglingSecurityRequirement(t *testing.T) {
	file := annotatedServiceFile()
	// Point the operation's security requirement at a scheme no document
	// annotation defines.
	method := file.Service[0].Method[0]
	method.Options.ProtoReflect().SetUnknown(nil)
	proto.SetExtension(method.Options, annotations.E_Http, &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Post{Post: "/v1/documents"},
		Body:    "*",
	})
	appendUnknownField(method.Options, rawBytesField(openapiOperationNumber,
		rawSubmessage(5, rawStringField(1, "ghost")))) // security { schemes: "ghost" }

	plugin := newOpenAPIPluginForFile(t, file, openapiAnnotationsFile())
	generator.Configure(plugin)
	err := generateOpenAPI(plugin, testConfig())
	if err == nil || !strings.Contains(err.Error(), `references scheme "ghost"`) {
		t.Fatalf("generateOpenAPI() error = %v, want the dangling-scheme failure", err)
	}
}

func TestGenerateOpenAPIRejectsFormlessSecurityScheme(t *testing.T) {
	file := annotatedServiceFile()
	// A document annotation whose scheme has a name but no oneof form.
	file.Options.ProtoReflect().SetUnknown(nil)
	appendUnknownField(file.Options, rawBytesField(openapiDocumentNumber,
		rawSubmessage(5, rawStringField(1, "empty"))))

	plugin := newOpenAPIPluginForFile(t, file, openapiAnnotationsFile())
	generator.Configure(plugin)
	err := generateOpenAPI(plugin, testConfig())
	if err == nil || !strings.Contains(err.Error(), "declares no scheme form") {
		t.Fatalf("generateOpenAPI() error = %v, want the formless-scheme failure", err)
	}
}

func generateAnnotatedDocument(t *testing.T, file *descriptorpb.FileDescriptorProto) (string, *highv3.Document) {
	t.Helper()
	return generateOpenAPIDocument(t, file, openapiAnnotationsFile())
}

// annotatedServiceFile builds the fixture:
//
//	option (sylphy.openapi.v1.document) = {
//	  title: "Document API"  version: "9.9.9"  description: "Manages documents."
//	  servers: {url: "https://api.example.com"}
//	  security_schemes: {name: "bearer" http_bearer: {bearer_format: "JWT"}}
//	  security_schemes: {name: "api_key" api_key_header: {header: "X-Api-Key"}}
//	};
//	service DocumentService {
//	  rpc GetDocument(GetDocumentRequest) returns (GetDocumentReply) {
//	    option (google.api.http) = {post: "/v1/documents", body: "*"};
//	    option (sylphy.openapi.v1.operation) = {
//	      summary: "Get one document"  tags: "documents"  deprecated: true
//	      security: {schemes: "bearer"}
//	    };
//	  }
//	}
//	message GetDocumentRequest {
//	  option (sylphy.openapi.v1.schema) = {description: "Request to fetch one document."};
//	  string name = 1 [(sylphy.openapi.v1.field) = {
//	    description: "Resource name of the document."  example: "documents/1"
//	  }];
//	}
func annotatedServiceFile() *descriptorpb.FileDescriptorProto {
	fileOptions := &descriptorpb.FileOptions{
		GoPackage: proto.String("example.com/test/v1;testv1"),
	}
	appendUnknownField(fileOptions, rawBytesField(openapiDocumentNumber, joinRaw(
		rawStringField(1, "Document API"),
		rawStringField(2, "9.9.9"),
		rawStringField(3, "Manages documents."),
		rawSubmessage(4, rawStringField(1, "https://api.example.com")),
		rawSubmessage(5, joinRaw(
			rawStringField(1, "bearer"),
			rawSubmessage(3, rawStringField(1, "JWT")), // http_bearer{bearer_format: JWT}
		)),
		rawSubmessage(5, joinRaw(
			rawStringField(1, "api_key"),
			rawSubmessage(4, rawStringField(1, "X-Api-Key")), // api_key_header{header: X-Api-Key}
		)),
	)))

	methodOptions := httpRuleOptions(&annotations.HttpRule{
		Pattern: &annotations.HttpRule_Post{Post: "/v1/documents"},
		Body:    "*",
	})
	appendUnknownField(methodOptions, rawBytesField(openapiOperationNumber, joinRaw(
		rawStringField(1, "Get one document"),
		rawStringField(3, "documents"),
		rawVarintField(4, 1), // deprecated: true
		rawSubmessage(5, rawStringField(1, "bearer")),
	)))

	messageOptions := new(descriptorpb.MessageOptions)
	appendUnknownField(messageOptions, rawBytesField(openapiSchemaNumber,
		rawStringField(1, "Request to fetch one document.")))

	nameField := stringField("name", 1)
	nameField.Options = new(descriptorpb.FieldOptions)
	appendUnknownField(nameField.Options, rawBytesField(openapiFieldNumber, joinRaw(
		rawStringField(1, "Resource name of the document."),
		rawStringField(2, "documents/1"),
	)))

	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("test/v1/document.proto"),
		Package: proto.String("test.v1"),
		Syntax:  proto.String("proto3"),
		Dependency: []string{
			"google/api/annotations.proto",
			"sylphy/openapi/v1/annotations.proto",
		},
		Options: fileOptions,
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name:    proto.String("GetDocumentRequest"),
				Options: messageOptions,
				Field:   []*descriptorpb.FieldDescriptorProto{nameField},
			},
			{
				Name:  proto.String("GetDocumentReply"),
				Field: []*descriptorpb.FieldDescriptorProto{stringField("name", 1)},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("DocumentService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("GetDocument"),
						InputType:  proto.String(".test.v1.GetDocumentRequest"),
						OutputType: proto.String(".test.v1.GetDocumentReply"),
						Options:    methodOptions,
					},
				},
			},
		},
	}
}

// openapiAnnotationsFile is the sylphy/openapi/v1/annotations.proto
// descriptor, declared by hand because the published API module the plugin
// compiles against predates the vocabulary. The shapes and numbers mirror the
// proto source; production requests carry the real file because the
// application's buf build includes it.
func openapiAnnotationsFile() *descriptorpb.FileDescriptorProto {
	stringProto := func(name string, number int32) *descriptorpb.FieldDescriptorProto {
		return stringField(name, number)
	}
	repeatedMessage := func(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
		return repeatedField(name, number, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, typeName)
	}
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("sylphy/openapi/v1/annotations.proto"),
		Package:    proto.String("sylphy.openapi.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/protobuf/descriptor.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("github.com/sylphylabs/forge/api/openapi/v1;openapi"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Document"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("title", 1),
					stringProto("version", 2),
					stringProto("description", 3),
					repeatedMessage("servers", 4, ".sylphy.openapi.v1.Server"),
					repeatedMessage("security_schemes", 5, ".sylphy.openapi.v1.SecurityScheme"),
				},
			},
			{
				Name: proto.String("Server"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("url", 1),
					stringProto("description", 2),
				},
			},
			{
				Name: proto.String("Operation"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("summary", 1),
					stringProto("description", 2),
					repeatedField("tags", 3, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
					scalarField("deprecated", 4, descriptorpb.FieldDescriptorProto_TYPE_BOOL),
					repeatedMessage("security", 5, ".sylphy.openapi.v1.SecurityRequirement"),
				},
			},
			{
				Name: proto.String("Schema"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("description", 1),
				},
			},
			{
				Name: proto.String("Field"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("description", 1),
					stringProto("example", 2),
					stringProto("format", 3),
				},
			},
			{
				Name: proto.String("SecurityScheme"),
				OneofDecl: []*descriptorpb.OneofDescriptorProto{
					{Name: proto.String("scheme")},
				},
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("name", 1),
					stringProto("description", 2),
					oneofMessageField("http_bearer", 3, ".sylphy.openapi.v1.HTTPBearer"),
					oneofMessageField("api_key_header", 4, ".sylphy.openapi.v1.APIKeyHeader"),
				},
			},
			{
				Name: proto.String("HTTPBearer"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("bearer_format", 1),
				},
			},
			{
				Name: proto.String("APIKeyHeader"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringProto("header", 1),
				},
			},
			{
				Name: proto.String("SecurityRequirement"),
				Field: []*descriptorpb.FieldDescriptorProto{
					repeatedField("schemes", 1, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
				},
			},
		},
		Extension: []*descriptorpb.FieldDescriptorProto{
			{
				Name:     proto.String("document"),
				Number:   proto.Int32(openapiDocumentNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".sylphy.openapi.v1.Document"),
				Extendee: proto.String(".google.protobuf.FileOptions"),
			},
			{
				Name:     proto.String("operation"),
				Number:   proto.Int32(openapiOperationNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".sylphy.openapi.v1.Operation"),
				Extendee: proto.String(".google.protobuf.MethodOptions"),
			},
			{
				Name:     proto.String("schema"),
				Number:   proto.Int32(openapiSchemaNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".sylphy.openapi.v1.Schema"),
				Extendee: proto.String(".google.protobuf.MessageOptions"),
			},
			{
				Name:     proto.String("field"),
				Number:   proto.Int32(openapiFieldNumber),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
				TypeName: proto.String(".sylphy.openapi.v1.Field"),
				Extendee: proto.String(".google.protobuf.FieldOptions"),
			},
		},
	}
}

// oneofMessageField builds a message field that is a member of the message's
// first oneof.
func oneofMessageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	field := messageField(name, number, typeName)
	field.OneofIndex = proto.Int32(0)
	return field
}

// rawStringField encodes one string field as raw proto wire bytes.
func rawStringField(number protowire.Number, value string) []byte {
	raw := protowire.AppendTag(nil, number, protowire.BytesType)
	return protowire.AppendString(raw, value)
}

// rawSubmessage encodes an embedded message field as raw proto wire bytes.
func rawSubmessage(number protowire.Number, payload []byte) []byte {
	return rawBytesField(number, payload)
}

// joinRaw concatenates raw wire fragments.
func joinRaw(fragments ...[]byte) []byte {
	var raw []byte
	for _, fragment := range fragments {
		raw = append(raw, fragment...)
	}
	return raw
}
