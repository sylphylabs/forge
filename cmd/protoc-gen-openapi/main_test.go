package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/pb33f/libopenapi"
	base "github.com/pb33f/libopenapi/datamodel/high/base"
	highv3 "github.com/pb33f/libopenapi/datamodel/high/v3"
	"github.com/pb33f/libopenapi/orderedmap"
	"github.com/santhosh-tekuri/jsonschema/v6"
	forgeerrors "github.com/sylphylabs/forge/errors"
	forgehttp "github.com/sylphylabs/forge/transport/http"
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

func TestGenerateOpenAPI32UsesForgeProblem(t *testing.T) {
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
		"ForgeProblem:",
		"Forge error response",
		"$ref: '#/components/schemas/ForgeProblem'",
		"application/problem+json:",
		"kind:",
		"reason:",
		"trace_id:",
		"violations:",
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

// TestGenerateOpenAPIDefaultResponseDisabled locks the default_response
// option: with it off, no operation carries a default response and the error
// component is not emitted unless something else references it.
func TestGenerateOpenAPIDefaultResponseDisabled(t *testing.T) {
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
	document := parseDocument(t, content)
	operation := findOperation(t, document, "/v1/hello/{name}", "GET")
	if operation.Responses.Default != nil {
		t.Fatal("operation still carries a default response")
	}
	validateOpenAPI32(t, content)
}

// TestGenerateOpenAPIIsDeterministic locks byte determinism end to end: two
// runs over an equal request produce identical output files.
func TestGenerateOpenAPIIsDeterministic(t *testing.T) {
	generate := func() string {
		plugin := newOpenAPIPluginForFile(t, projectionTestFile())
		generator.Configure(plugin)
		if err := generateOpenAPI(plugin, testConfig()); err != nil {
			t.Fatalf("generateOpenAPI() error = %v", err)
		}
		return plugin.Response().File[0].GetContent()
	}
	first := generate()
	for i := 0; i < 4; i++ {
		if next := generate(); next != first {
			t.Fatalf("generation is not deterministic:\nfirst:\n%s\nnext:\n%s", first, next)
		}
	}
}

func TestGenerateOpenAPIProjectsBodyAndResponseSchemas(t *testing.T) {
	content, document := generateOpenAPIDocument(t, projectionTestFile())

	scalar := requestBodySchema(t, findOperation(t, document, "/v1/scalar/{name}", "POST"))
	if schemaType(scalar) != "integer" || scalar.Format != "int32" {
		t.Fatalf("scalar request schema = type %q format %q, want integer/int32", schemaType(scalar), scalar.Format)
	}

	repeated := requestBodySchema(t, findOperation(t, document, "/v1/repeated/{name}", "POST"))
	if schemaType(repeated) != "array" || repeated.Items == nil {
		t.Fatalf("repeated request schema = %+v, want array with items", repeated)
	}

	mapped := requestBodySchema(t, findOperation(t, document, "/v1/mapped/{name}", "POST"))
	if schemaType(mapped) != "object" || mapped.AdditionalProperties == nil {
		t.Fatalf("map request schema = %+v, want object with additionalProperties", mapped)
	}

	response := findOperationResponse(t, findOperation(t, document, "/v1/scalar/{name}", "POST"), "200")
	responseSchema := mediaTypeSchema(t, response.Content, "application/json")
	if schemaType(responseSchema) != "string" {
		t.Fatalf("projected response schema type = %q, want string", schemaType(responseSchema))
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

	requestBody := operation.RequestBody
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
			wantErr: `HTTP method "QUERY" is not emitted`,
		},
		{
			name: "arbitrary method",
			file: bindingTestFile(&annotations.HttpRule{
				Pattern: &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "REPORT", Path: "/v1/report/{name}"}},
			}),
			wantErr: `HTTP method "REPORT" is not emitted`,
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

// TestForgeProblemSchemaMatchesRuntimeWireFormat locks the generated
// ForgeProblem schema to the runtime error encoder. It renders a full-field
// error through the published forge runtime's DefaultErrorEncoder and asserts
// that the JSON keys on the wire are exactly the properties the generator
// documents, so a runtime field addition or rename turns this test red.
func TestForgeProblemSchemaMatchesRuntimeWireFormat(t *testing.T) {
	_, document := generateOpenAPIDocument(t, projectionTestFile())

	problemSchema := findComponentSchema(t, document, openapigen.DefaultErrorSchemaName)
	schemaKeys := propertyNames(t, problemSchema)
	violationsProperty := findProperty(t, problemSchema, "violations")
	if violationsProperty.Items == nil || violationsProperty.Items.A == nil {
		t.Fatal("violations property has no item schema")
	}
	violationKeys := propertyNames(t, violationsProperty.Items.A.Schema())

	public := forgeerrors.Public{
		Kind:     forgeerrors.KindInvalidArgument,
		Domain:   "test.contract.v1",
		Reason:   "CONTRACT_FAILURE_REASON_INVALID",
		Message:  "field validation failed",
		Metadata: map[string]string{"tenant": "t1"},
		TraceID:  "4bf92f3577b34da6a3ce929d0e0e4736",
		Violations: []forgeerrors.Violation{
			{Field: "user.email", Description: "malformed"},
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/v1/contract", nil)
	forgehttp.DefaultErrorEncoder(recorder, request, forgeerrors.FromPublic(public))

	mediaTypes := errorResponseMediaTypes(t, document)
	if orderedmap.Len(mediaTypes) != 1 {
		t.Fatalf("error response media types = %d, want 1", orderedmap.Len(mediaTypes))
	}
	documentedType := mediaTypes.First().Key()
	if contentType := recorder.Header().Get("Content-Type"); contentType != documentedType {
		t.Fatalf("runtime Content-Type = %q, OpenAPI media type = %q", contentType, documentedType)
	}

	var wire map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode runtime error body %q: %v", recorder.Body.String(), err)
	}
	wireKeys := sortedKeys(wire)
	if !slices.Equal(wireKeys, schemaKeys) {
		t.Fatalf("runtime wire keys = %v, OpenAPI %s properties = %v", wireKeys, openapigen.DefaultErrorSchemaName, schemaKeys)
	}

	var wireViolations []map[string]json.RawMessage
	if err := json.Unmarshal(wire["violations"], &wireViolations); err != nil {
		t.Fatalf("decode violations: %v", err)
	}
	if len(wireViolations) != 1 {
		t.Fatalf("wire violations = %d, want 1", len(wireViolations))
	}
	if wireViolationKeys := sortedKeys(wireViolations[0]); !slices.Equal(wireViolationKeys, violationKeys) {
		t.Fatalf("runtime violation keys = %v, OpenAPI violation properties = %v", wireViolationKeys, violationKeys)
	}
}

// schemaType returns the single JSON Schema type of a parsed schema, or ""
// when none is declared.
func schemaType(schema *base.Schema) string {
	if schema == nil || len(schema.Type) != 1 {
		return ""
	}
	return schema.Type[0]
}

// findComponentSchema returns the named schema from document components.
func findComponentSchema(t *testing.T, document *highv3.Document, name string) *base.Schema {
	t.Helper()

	proxy := document.Components.Schemas.GetOrZero(name)
	if proxy == nil {
		t.Fatalf("component schema %q not found", name)
	}
	schema := proxy.Schema()
	if schema == nil {
		t.Fatalf("component schema %q did not build: %v", name, proxy.GetBuildError())
	}
	return schema
}

// findProperty returns the named property schema of an object schema.
func findProperty(t *testing.T, schema *base.Schema, name string) *base.Schema {
	t.Helper()

	proxy := schema.Properties.GetOrZero(name)
	if proxy == nil {
		t.Fatalf("property %q not found", name)
	}
	return proxy.Schema()
}

// propertyNames returns the sorted property names of an object schema.
func propertyNames(t *testing.T, schema *base.Schema) []string {
	t.Helper()

	if orderedmap.Len(schema.Properties) == 0 {
		t.Fatal("schema declares no properties")
	}
	names := make([]string, 0, orderedmap.Len(schema.Properties))
	for name := range schema.Properties.KeysFromOldest() {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// errorResponseMediaTypes returns the media types of the first default error
// response in the document.
func errorResponseMediaTypes(t *testing.T, document *highv3.Document) *orderedmap.Map[string, *highv3.MediaType] {
	t.Helper()

	for pathItem := range document.Paths.PathItems.ValuesFromOldest() {
		for _, operation := range []*highv3.Operation{pathItem.Get, pathItem.Post} {
			if operation == nil || operation.Responses == nil || operation.Responses.Default == nil {
				continue
			}
			return operation.Responses.Default.Content
		}
	}
	t.Fatal("no default error response found")
	return nil
}

// sortedKeys returns the sorted keys of a decoded JSON object.
func sortedKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func findOperation(t *testing.T, document *highv3.Document, path, method string) *highv3.Operation {
	t.Helper()

	pathItem := document.Paths.PathItems.GetOrZero(path)
	if pathItem == nil {
		t.Fatalf("path %q not found", path)
	}
	var operation *highv3.Operation
	switch method {
	case "GET":
		operation = pathItem.Get
	case "POST":
		operation = pathItem.Post
	case "PUT":
		operation = pathItem.Put
	case "DELETE":
		operation = pathItem.Delete
	case "OPTIONS":
		operation = pathItem.Options
	case "HEAD":
		operation = pathItem.Head
	case "PATCH":
		operation = pathItem.Patch
	case "TRACE":
		operation = pathItem.Trace
	default:
		t.Fatalf("unsupported test method %q", method)
	}
	if operation == nil {
		t.Fatalf("operation %s %s is nil", method, path)
	}
	return operation
}

func findOperationResponse(t *testing.T, operation *highv3.Operation, name string) *highv3.Response {
	t.Helper()

	response := findOptionalResponse(operation, name)
	if response == nil {
		t.Fatalf("response %q not found", name)
	}
	return response
}

func findOptionalResponse(operation *highv3.Operation, name string) *highv3.Response {
	if operation.Responses == nil {
		return nil
	}
	if name == "default" {
		return operation.Responses.Default
	}
	return operation.Responses.Codes.GetOrZero(name)
}

func hasParameter(operation *highv3.Operation, name, location string) bool {
	for _, parameter := range operation.Parameters {
		if parameter.Name == name && parameter.In == location {
			return true
		}
	}
	return false
}

func requestBodySchema(t *testing.T, operation *highv3.Operation) *base.Schema {
	t.Helper()

	if operation.RequestBody == nil {
		t.Fatal("request body is nil")
	}
	return mediaTypeSchema(t, operation.RequestBody.Content, "application/json")
}

func mediaTypeSchema(t *testing.T, mediaTypes *orderedmap.Map[string, *highv3.MediaType], name string) *base.Schema {
	t.Helper()

	proxy := mediaType(t, mediaTypes, name).Schema
	if proxy == nil {
		t.Fatalf("media type %q has no schema", name)
	}
	schema := proxy.Schema()
	if schema == nil {
		t.Fatalf("media type %q schema did not build: %v", name, proxy.GetBuildError())
	}
	return schema
}

func mediaType(t *testing.T, mediaTypes *orderedmap.Map[string, *highv3.MediaType], name string) *highv3.MediaType {
	t.Helper()

	if mediaTypes == nil {
		t.Fatalf("media types are nil, want %q", name)
	}
	value := mediaTypes.GetOrZero(name)
	if value == nil {
		t.Fatalf("media type %q not found", name)
	}
	return value
}

func generateOpenAPIDocument(t *testing.T, file *descriptorpb.FileDescriptorProto, dependencies ...*descriptorpb.FileDescriptorProto) (string, *highv3.Document) {
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
	return content, parseDocument(t, content)
}

// parseDocument parses generated output with libopenapi, the independent
// parser the tests assert structure through.
func parseDocument(t *testing.T, content string) *highv3.Document {
	t.Helper()

	document, err := libopenapi.NewDocument([]byte(content))
	if err != nil {
		t.Fatalf("parse generated OpenAPI: %v", err)
	}
	model, err := document.BuildV3Model()
	if err != nil {
		t.Fatalf("build OpenAPI model: %v", err)
	}
	return &model.Model
}

func validateOpenAPI32(t *testing.T, content string) {
	t.Helper()

	document, err := libopenapi.NewDocument([]byte(content))
	if err != nil {
		t.Fatalf("parse generated OpenAPI with libopenapi: %v", err)
	}
	if document.GetSpecInfo().Version != openapigen.DefaultOpenAPIVersion {
		t.Fatalf("independent parser version = %q, want %q", document.GetSpecInfo().Version, openapigen.DefaultOpenAPIVersion)
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

	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/greeter.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("SayHelloRequest"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("name", 1),
				},
			},
			{
				Name: proto.String("SayHelloReply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					stringField("message", 1),
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

	protoFiles := make([]*descriptorpb.FileDescriptorProto, 0, 4+len(dependencies)+1)
	protoFiles = append(protoFiles,
		protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
		protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
		protodesc.ToFileDescriptorProto(annotations.File_google_api_http_proto),
		protodesc.ToFileDescriptorProto(annotations.File_google_api_annotations_proto),
	)
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
	openapiVersion := openapigen.DefaultOpenAPIVersion
	version := "v1"
	title := "Test API"
	description := ""
	naming := "proto"
	fqSchemaNaming := true
	enumType := "string"
	circularDepth := 2
	defaultResponse := true
	errorSchemaName := openapigen.DefaultErrorSchemaName
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
