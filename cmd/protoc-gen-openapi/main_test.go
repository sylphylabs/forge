package main

import (
	"strings"
	"testing"

	v3 "github.com/google/gnostic/openapiv3"
	"github.com/openkratos/kratos/cmd/internal/generator"
	openapigen "github.com/openkratos/kratos/cmd/internal/openapi/generator"
	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerateOpenAPI32UsesOpenKratosErrorEnvelope(t *testing.T) {
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
		"openkratos.errors.v1.Status:",
		"description: OpenKratos HTTP JSON error envelope",
		"$ref: '#/components/schemas/openkratos.errors.v1.Status'",
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
	if !strings.Contains(content, "\"401\":") || !strings.Contains(content, "$ref: '#/components/schemas/openkratos.errors.v1.Status'") {
		t.Fatalf("annotated 401 response did not receive OpenKratos error content:\n%s", content)
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
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(anypb.File_google_protobuf_any_proto),
			protodesc.ToFileDescriptorProto(annotations.File_google_api_http_proto),
			protodesc.ToFileDescriptorProto(annotations.File_google_api_annotations_proto),
			protodesc.ToFileDescriptorProto(v3.File_openapiv3_OpenAPIv3_proto),
			protodesc.ToFileDescriptorProto(v3.File_openapiv3_annotations_proto),
			file,
		},
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
