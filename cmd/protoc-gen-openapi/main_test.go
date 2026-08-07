package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	v3 "github.com/google/gnostic/openapiv3"
	"github.com/pb33f/libopenapi"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/sylphylabs/forge/cmd/internal/generator"
	openapigen "github.com/sylphylabs/forge/cmd/internal/openapi/generator"
)

func TestGenerateOpenAPI32UsesForgeErrorEnvelope(t *testing.T) {
	plugin := newOpenAPIPlugin(t)
	generator.Configure(plugin)
	if err := generateOpenAPI(plugin, testConfig()); err != nil {
		t.Fatalf("generateOpenAPI() error = %v", err)
	}

	response := plugin.Response()
	if response.GetError() != "" {
		t.Fatalf("generation error = %s", response.GetError())
	}
	if len(response.File) != 1 {
		t.Fatalf("generated files = %d, want 1", len(response.File))
	}
	content := response.File[0].GetContent()
	for _, want := range []string{
		"openapi: 3.2.0",
		"sylphy.errors.v1.Status:",
		"description: Forge HTTP JSON error envelope",
		"$ref: '#/components/schemas/sylphy.errors.v1.Status'",
		"reason:",
		"metadata:",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated OpenAPI is missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "google.rpc.Status:") {
		t.Fatalf("generated OpenAPI should not use google.rpc.Status for default errors:\n%s", content)
	}
	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIPatchesAnnotatedErrorResponses(t *testing.T) {
	plugin := newOpenAPIPlugin(t)
	generator.Configure(plugin)
	conf := testConfig()
	defaultResponse := false
	conf.DefaultResponse = &defaultResponse
	if err := generateOpenAPI(plugin, conf); err != nil {
		t.Fatalf("generateOpenAPI() error = %v", err)
	}

	content := plugin.Response().File[0].GetContent()
	if strings.Contains(content, "default:") {
		t.Fatalf("default response should be disabled:\n%s", content)
	}
	if !strings.Contains(content, "\"401\":") || !strings.Contains(content, "$ref: '#/components/schemas/sylphy.errors.v1.Status'") {
		t.Fatalf("annotated 401 response did not receive Forge error content:\n%s", content)
	}

	document, err := v3.ParseDocument([]byte(content))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	response := findResponse(t, document, "409")
	mediaTypes := response.GetContent().GetAdditionalProperties()
	if len(mediaTypes) != 1 || mediaTypes[0].GetName() != "application/problem+json" {
		t.Fatalf("annotated 409 content = %v, want application/problem+json", mediaTypes)
	}
}

func TestGenerateOpenAPIProjectsBodyAndResponseSchemas(t *testing.T) {
	content, document := generateOpenAPIDocument(t, projectionTestFile())

	scalar := requestBodySchema(t, findOperation(t, document, "/v1/scalar/{name}", "POST"))
	if scalar.Type != "integer" || scalar.Format != "int32" {
		t.Fatalf("scalar request schema = type %q format %q, want integer/int32", scalar.Type, scalar.Format)
	}

	repeated := requestBodySchema(t, findOperation(t, document, "/v1/repeated/{name}", "POST"))
	if repeated.Type != "array" || repeated.Items == nil {
		t.Fatalf("repeated request schema = %+v, want array with items", repeated)
	}

	mapped := requestBodySchema(t, findOperation(t, document, "/v1/mapped/{name}", "POST"))
	if mapped.Type != "object" || mapped.AdditionalProperties == nil {
		t.Fatalf("map request schema = %+v, want object with additionalProperties", mapped)
	}

	response := findOperationResponse(t, findOperation(t, document, "/v1/scalar/{name}", "POST"), "200")
	responseSchema := mediaTypeSchema(t, response.Content, "application/json")
	if responseSchema.Type != "string" {
		t.Fatalf("projected response schema type = %q, want string", responseSchema.Type)
	}

	nested := findOperation(t, document, "/v1/nested/{resource.name}", "GET")
	if !hasParameter(nested, "resource.name", "path") {
		t.Fatal("nested path field is missing its path parameter")
	}
	if hasParameter(nested, "resource.name", "query") {
		t.Fatal("nested path field was duplicated as a query parameter")
	}
	if !hasParameter(nested, "resource.zone", "query") {
		t.Fatal("unbound nested field is missing its query parameter")
	}

	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIUsesHTTPBodyMediaType(t *testing.T) {
	file := httpBodyTestFile()
	content, document := generateOpenAPIDocument(t, file,
		protodesc.ToFileDescriptorProto(httpbody.File_google_api_httpbody_proto))
	operation := findOperation(t, document, "/v1/media/{name}", "POST")

	requestBody := operation.RequestBody.GetRequestBody()
	if requestBody == nil {
		t.Fatal("request body is nil")
	}
	if mediaType(t, requestBody.Content, "*/*").Schema != nil {
		t.Fatal("HttpBody request media type should not declare a JSON schema")
	}

	response := findOperationResponse(t, operation, "200")
	if mediaType(t, response.Content, "*/*").Schema != nil {
		t.Fatal("HttpBody response media type should not declare a JSON schema")
	}

	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPISupportsStandardCustomMethods(t *testing.T) {
	rule := &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{
			Kind: "HEAD",
			Path: "/v1/head/{name}",
		}},
		AdditionalBindings: []*annotations.HttpRule{
			{Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "OPTIONS", Path: "/v1/options/{name}"}}},
			{Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "TRACE", Path: "/v1/trace/{name}"}}},
		},
	}
	content, document := generateOpenAPIDocument(t, bindingTestFile(rule))

	findOperation(t, document, "/v1/head/{name}", "HEAD")
	findOperation(t, document, "/v1/options/{name}", "OPTIONS")
	findOperation(t, document, "/v1/trace/{name}", "TRACE")
	validateOpenAPI32(t, content)
}

func TestGenerateOpenAPIRejectsInvalidBindings(t *testing.T) {
	tests := []struct {
		name    string
		file    *descriptorpb.FileDescriptorProto
		wantErr string
	}{
		{
			name: "nested additional binding",
			file: bindingTestFile(&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Get{Get: "/v1/{name}"},
				AdditionalBindings: []*annotations.HttpRule{
					{
						Pattern: &annotations.HttpRule_Get{Get: "/v1/alt/{name}"},
						AdditionalBindings: []*annotations.HttpRule{
							{Pattern: &annotations.HttpRule_Get{Get: "/v1/nested/{name}"}},
						},
					},
				},
			}),
			wantErr: "nested additional bindings are not allowed",
		},
		{
			name: "conflicting routes",
			file: bindingTestFile(
				&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/v1/{name}/tail"}},
				&annotations.HttpRule{Pattern: &annotations.HttpRule_Get{Get: "/v1/head/{other}"}},
			),
			wantErr: "conflicting HTTP rule",
		},
		{
			name: "query method",
			file: bindingTestFile(&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "QUERY", Path: "/v1/query/{name}"}},
			}),
			wantErr: `HTTP method "QUERY" cannot be represented`,
		},
		{
			name: "arbitrary method",
			file: bindingTestFile(&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "REPORT", Path: "/v1/report/{name}"}},
			}),
			wantErr: `HTTP method "REPORT" cannot be represented`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plugin := newOpenAPIPluginForFile(t, tt.file)
			generator.Configure(plugin)
			err := generateOpenAPI(plugin, testConfig())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("generateOpenAPI() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func findResponse(t *testing.T, document *v3.Document, name string) *v3.Response {
	t.Helper()

	for _, path := range document.GetPaths().GetPath() {
		operation := path.GetValue().GetGet()
		if operation == nil {
			continue
		}
		for _, response := range operation.GetResponses().GetResponseOrReference() {
			if response.GetName() == name {
				return response.GetValue().GetResponse()
			}
		}
	}
	t.Fatalf("response %q not found", name)
	return nil
}

func findOperation(t *testing.T, document *v3.Document, path, method string) *v3.Operation {
	t.Helper()

	for _, namedPath := range document.GetPaths().GetPath() {
		if namedPath.GetName() != path {
			continue
		}
		pathItem := namedPath.GetValue()
		var operation *v3.Operation
		switch method {
		case "GET":
			operation = pathItem.GetGet()
		case "POST":
			operation = pathItem.GetPost()
		case "PUT":
			operation = pathItem.GetPut()
		case "DELETE":
			operation = pathItem.GetDelete()
		case "OPTIONS":
			operation = pathItem.GetOptions()
		case "HEAD":
			operation = pathItem.GetHead()
		case "PATCH":
			operation = pathItem.GetPatch()
		case "TRACE":
			operation = pathItem.GetTrace()
		default:
			t.Fatalf("unsupported test method %q", method)
		}
		if operation == nil {
			t.Fatalf("operation %s %s is nil", method, path)
		}
		return operation
	}
	t.Fatalf("path %q not found", path)
	return nil
}

func findOperationResponse(t *testing.T, operation *v3.Operation, name string) *v3.Response {
	t.Helper()

	for _, response := range operation.GetResponses().GetResponseOrReference() {
		if response.GetName() == name {
			return response.GetValue().GetResponse()
		}
	}
	t.Fatalf("response %q not found", name)
	return nil
}

func hasParameter(operation *v3.Operation, name, location string) bool {
	for _, parameterOrReference := range operation.GetParameters() {
		parameter := parameterOrReference.GetParameter()
		if parameter.GetName() == name && parameter.GetIn() == location {
			return true
		}
	}
	return false
}

func requestBodySchema(t *testing.T, operation *v3.Operation) *v3.Schema {
	t.Helper()

	requestBody := operation.GetRequestBody().GetRequestBody()
	if requestBody == nil {
		t.Fatal("request body is nil")
	}
	return mediaTypeSchema(t, requestBody.Content, "application/json")
}

func mediaTypeSchema(t *testing.T, mediaTypes *v3.MediaTypes, name string) *v3.Schema {
	t.Helper()

	schema := mediaType(t, mediaTypes, name).GetSchema().GetSchema()
	if schema == nil {
		t.Fatalf("media type %q has no inline schema", name)
	}
	return schema
}

func mediaType(t *testing.T, mediaTypes *v3.MediaTypes, name string) *v3.MediaType {
	t.Helper()

	if mediaTypes == nil {
		t.Fatalf("media types are nil, want %q", name)
	}
	for _, namedMediaType := range mediaTypes.GetAdditionalProperties() {
		if namedMediaType.GetName() == name {
			return namedMediaType.GetValue()
		}
	}
	t.Fatalf("media type %q not found", name)
	return nil
}

func generateOpenAPIDocument(t *testing.T, file *descriptorpb.FileDescriptorProto, dependencies ...*descriptorpb.FileDescriptorProto) (string, *v3.Document) {
	t.Helper()

	plugin := newOpenAPIPluginForFile(t, file, dependencies...)
	generator.Configure(plugin)
	if err := generateOpenAPI(plugin, testConfig()); err != nil {
		t.Fatalf("generateOpenAPI() error = %v", err)
	}
	response := plugin.Response()
	if response.GetError() != "" {
		t.Fatalf("generation error = %s", response.GetError())
	}
	if len(response.File) != 1 {
		t.Fatalf("generated files = %d, want 1", len(response.File))
	}
	content := response.File[0].GetContent()
	document, err := v3.ParseDocument([]byte(content))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	return content, document
}

func validateOpenAPI32(t *testing.T, content string) {
	t.Helper()

	document, err := libopenapi.NewDocument([]byte(content))
	if err != nil {
		t.Fatalf("parse generated OpenAPI with libopenapi: %v", err)
	}
	if document.GetSpecInfo().Version != defaultOpenAPIVersion {
		t.Fatalf("independent parser version = %q, want %q", document.GetSpecInfo().Version, defaultOpenAPIVersion)
	}
	compiler := jsonschema.NewCompiler()
	officialSchema, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(document.GetSpecInfo().APISchema))
	if err != nil {
		t.Fatalf("decode official OpenAPI 3.2 schema: %v", err)
	}
	const schemaURL = "https://spec.openapis.org/oas/3.2/schema/2025-09-17"
	if err = compiler.AddResource(schemaURL, officialSchema); err != nil {
		t.Fatalf("load official OpenAPI 3.2 schema: %v", err)
	}
	schema, err := compiler.Compile(schemaURL)
	if err != nil {
		t.Fatalf("compile official OpenAPI 3.2 schema: %v", err)
	}
	documentJSON := document.GetSpecInfo().GetSpecJSONBytes()
	if documentJSON == nil {
		t.Fatal("independent parser did not produce a JSON representation")
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(*documentJSON))
	if err != nil {
		t.Fatalf("decode generated OpenAPI as JSON: %v", err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("generated document does not validate against the official OpenAPI 3.2 schema: %v", err)
	}
}

func projectionTestFile() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/projection.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Resource"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					stringField("zone", 2),
				},
			},
			{
				Name: proto.String("NestedRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("resource", 1, ".test.v1.Resource"),
				},
			},
			{
				Name: proto.String("ScalarRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					scalarField("value", 2, descriptorpb.FieldDescriptorProto_TYPE_INT32),
				},
			},
			{
				Name: proto.String("RepeatedRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					repeatedField("values", 2, descriptorpb.FieldDescriptorProto_TYPE_STRING, ""),
				},
			},
			{
				Name: proto.String("MapRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					repeatedField("values", 2, descriptorpb.FieldDescriptorProto_TYPE_MESSAGE, ".test.v1.MapRequest.ValuesEntry"),
				},
				NestedType: []*descriptorpb.DescriptorProto{
					{
						Name:    proto.String("ValuesEntry"),
						Options: &descriptorpb.MessageOptions{MapEntry: proto.Bool(true)},
						Field: []*descriptorpb.FieldDescriptorProto{
							stringField("key", 1),
							stringField("value", 2),
						},
					},
				},
			},
			{
				Name: proto.String("Reply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("result", 1),
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Projection"),
				Method: []*descriptorpb.MethodDescriptorProto{
					httpMethod("Nested", ".test.v1.NestedRequest", ".test.v1.Reply", &annotations.HttpRule{
						Pattern: &annotations.HttpRule_Get{Get: "/v1/nested/{resource.name}"},
					}),
					httpMethod("Scalar", ".test.v1.ScalarRequest", ".test.v1.Reply", &annotations.HttpRule{
						Pattern:      &annotations.HttpRule_Post{Post: "/v1/scalar/{name}"},
						Body:         "value",
						ResponseBody: "result",
					}),
					httpMethod("Repeated", ".test.v1.RepeatedRequest", ".test.v1.Reply", &annotations.HttpRule{
						Pattern: &annotations.HttpRule_Post{Post: "/v1/repeated/{name}"},
						Body:    "values",
					}),
					httpMethod("Mapped", ".test.v1.MapRequest", ".test.v1.Reply", &annotations.HttpRule{
						Pattern: &annotations.HttpRule_Post{Post: "/v1/mapped/{name}"},
						Body:    "values",
					}),
				},
			},
		},
	}
}

func httpBodyTestFile() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/http_body.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto", "google/api/httpbody.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("MediaRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					messageField("body", 2, ".google.api.HttpBody"),
				},
			},
			{
				Name: proto.String("MediaReply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					messageField("body", 1, ".google.api.HttpBody"),
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Media"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("Upload"),
						InputType:  proto.String(".test.v1.MediaRequest"),
						OutputType: proto.String(".test.v1.MediaReply"),
						Options: httpRuleOptions(&annotations.HttpRule{
							Pattern:      &annotations.HttpRule_Post{Post: "/v1/media/{name}"},
							Body:         "body",
							ResponseBody: "body",
						}),
					},
				},
			},
		},
	}
}

func bindingTestFile(rules ...*annotations.HttpRule) *descriptorpb.FileDescriptorProto {
	methods := make([]*descriptorpb.MethodDescriptorProto, 0, len(rules))
	for i, rule := range rules {
		methods = append(methods, &descriptorpb.MethodDescriptorProto{
			Name:       proto.String(fmt.Sprintf("Call%d", i+1)),
			InputType:  proto.String(".test.v1.BindingRequest"),
			OutputType: proto.String(".test.v1.BindingReply"),
			Options:    httpRuleOptions(rule),
		})
	}
	return &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/binding.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("BindingRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
					stringField("other", 2),
				},
			},
			{
				Name: proto.String("BindingReply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("result", 1),
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{{Name: proto.String("Bindings"), Method: methods}},
	}
}

// stringField builds an optional proto3 string field descriptor.
func stringField(name string, number int32) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
	}
}

// scalarField builds an optional proto3 field descriptor of the given scalar type.
func scalarField(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:   kind.Enum(),
	}
}

// messageField builds an optional proto3 message-typed field descriptor.
func messageField(name string, number int32, typeName string) *descriptorpb.FieldDescriptorProto {
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(typeName),
	}
}

// repeatedField builds a repeated proto3 field descriptor of the given type.
func repeatedField(name string, number int32, kind descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
	field := &descriptorpb.FieldDescriptorProto{
		Name:   proto.String(name),
		Number: proto.Int32(number),
		Label:  descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:   kind.Enum(),
	}
	if typeName != "" {
		field.TypeName = proto.String(typeName)
	}
	return field
}

// httpMethod builds a method descriptor carrying the given google.api.http rule.
func httpMethod(name, inputType, outputType string, rule *annotations.HttpRule) *descriptorpb.MethodDescriptorProto {
	return &descriptorpb.MethodDescriptorProto{
		Name:       proto.String(name),
		InputType:  proto.String(inputType),
		OutputType: proto.String(outputType),
		Options:    httpRuleOptions(rule),
	}
}

func httpRuleOptions(rule *annotations.HttpRule) *descriptorpb.MethodOptions {
	options := new(descriptorpb.MethodOptions)
	proto.SetExtension(options, annotations.E_Http, rule)
	return options
}

func newOpenAPIPlugin(t *testing.T) *protogen.Plugin {
	t.Helper()

	methodOptions := new(descriptorpb.MethodOptions)
	proto.SetExtension(methodOptions, annotations.E_Http, &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Get{Get: "/v1/hello/{name}"},
	})
	proto.SetExtension(methodOptions, v3.E_Operation, &v3.Operation{
		Responses: &v3.Responses{
			ResponseOrReference: []*v3.NamedResponseOrReference{
				{
					Name: "401",
					Value: &v3.ResponseOrReference{
						Oneof: &v3.ResponseOrReference_Response{
							Response: &v3.Response{Description: "unauthenticated"},
						},
					},
				},
				{
					Name: "409",
					Value: &v3.ResponseOrReference{
						Oneof: &v3.ResponseOrReference_Response{
							Response: &v3.Response{
								Description: "conflict",
								Content: &v3.MediaTypes{
									AdditionalProperties: []*v3.NamedMediaType{
										{
											Name: "application/problem+json",
											Value: &v3.MediaType{
												Schema: &v3.SchemaOrReference{
													Oneof: &v3.SchemaOrReference_Schema{
														Schema: &v3.Schema{Type: "string"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	})

	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/greeter.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto", "openapiv3/annotations.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("SayHelloRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("name"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
				},
			},
			{
				Name: proto.String("SayHelloReply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("message"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("Greeter"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("SayHello"),
						InputType:  proto.String(".test.v1.SayHelloRequest"),
						OutputType: proto.String(".test.v1.SayHelloReply"),
						Options:    methodOptions,
					},
				},
			},
		},
	}
	return newOpenAPIPluginForFile(t, file)
}

func newOpenAPIPluginForFile(t *testing.T, file *descriptorpb.FileDescriptorProto, dependencies ...*descriptorpb.FileDescriptorProto) *protogen.Plugin {
	t.Helper()

	protoFiles := []*descriptorpb.FileDescriptorProto{
		protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
		protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
		protodesc.ToFileDescriptorProto(annotations.File_google_api_http_proto),
		protodesc.ToFileDescriptorProto(annotations.File_google_api_annotations_proto),
		protodesc.ToFileDescriptorProto(v3.File_openapiv3_OpenAPIv3_proto),
		protodesc.ToFileDescriptorProto(v3.File_openapiv3_annotations_proto),
	}
	protoFiles = append(protoFiles, dependencies...)
	protoFiles = append(protoFiles, file)
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file.GetName()},
		ProtoFile:      protoFiles,
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatalf("protogen.Options.New() error = %v", err)
	}
	return plugin
}

func testConfig() openapigen.Configuration {
	openapiVersion := defaultOpenAPIVersion
	version := "v1"
	title := "Test API"
	description := ""
	naming := "proto"
	fqSchemaNaming := true
	enumType := "string"
	circularDepth := 2
	defaultResponse := true
	errorSchemaName := defaultErrorSchemaName
	outputMode := "merged"
	return openapigen.Configuration{
		OpenAPIVersion:  &openapiVersion,
		Version:         &version,
		Title:           &title,
		Description:     &description,
		Naming:          &naming,
		FQSchemaNaming:  &fqSchemaNaming,
		EnumType:        &enumType,
		CircularDepth:   &circularDepth,
		DefaultResponse: &defaultResponse,
		ErrorSchemaName: &errorSchemaName,
		OutputMode:      &outputMode,
	}
}
