package httpbinding

import (
	"strings"
	"testing"

	"google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	"github.com/sylphylabs/forge/internal/httprule"
)

func TestAnalyze(t *testing.T) {
	rule := &annotations.HttpRule{
		Pattern:      &annotations.HttpRule_Post{Post: "/v1/{name}"},
		Body:         "data",
		ResponseBody: "result",
		AdditionalBindings: []*annotations.HttpRule{
			{
				Pattern:      &annotations.HttpRule_Custom{Custom: &annotations.CustomHttpPattern{Kind: "REPORT", Path: "/v1/report/{name}"}},
				ResponseBody: "result",
			},
		},
	}
	bindings, annotated, err := Analyze(testMethod(t, rule))
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !annotated {
		t.Fatal("Analyze() annotated = false, want true")
	}
	if len(bindings) != 2 {
		t.Fatalf("Analyze() bindings = %d, want 2", len(bindings))
	}
	primary := bindings[0]
	if primary.Method != "POST" || primary.Path != "/v1/{name}" || primary.Body != "data" || primary.ResponseBody != "result" {
		t.Fatalf("primary binding = %+v", primary)
	}
	if primary.BodyField == nil || primary.BodyField.FullName() != "test.Request.data" {
		t.Fatalf("primary body field = %v", primary.BodyField)
	}
	if primary.ResponseBodyField == nil || primary.ResponseBodyField.FullName() != "test.Reply.result" {
		t.Fatalf("primary response body field = %v", primary.ResponseBodyField)
	}
	if bindings[1].Index != 1 || bindings[1].Method != "REPORT" || bindings[1].Path != "/v1/report/{name}" {
		t.Fatalf("additional binding = %+v", bindings[1])
	}
}

func TestAnalyzeRejectsNestedAdditionalBinding(t *testing.T) {
	rule := &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Get{Get: "/v1/{name}"},
		AdditionalBindings: []*annotations.HttpRule{
			{
				Pattern: &annotations.HttpRule_Get{Get: "/v1/alt/{name}"},
				AdditionalBindings: []*annotations.HttpRule{
					{Pattern: &annotations.HttpRule_Get{Get: "/v1/nested/{name}"}},
				},
			},
		},
	}
	_, _, err := Analyze(testMethod(t, rule))
	if err == nil || !strings.Contains(err.Error(), "additional binding 1: nested additional bindings are not allowed") {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestAnalyzeRejectsBodyPathOverlap(t *testing.T) {
	rule := &annotations.HttpRule{
		Pattern: &annotations.HttpRule_Post{Post: "/v1/{data.value}"},
		Body:    "data",
	}
	_, _, err := Analyze(testMethod(t, rule))
	if err == nil || !strings.Contains(err.Error(), `body field "data" overlaps path field "data.value"`) {
		t.Fatalf("Analyze() error = %v", err)
	}
}

func TestSetRejectsDuplicateAndConflictingBindings(t *testing.T) {
	set := NewSet()
	first := testBinding(t, "GET", "/v1/{first}")
	if err := set.Add(first); err != nil {
		t.Fatalf("Set.Add(first) error = %v", err)
	}
	if err := set.Add(testBinding(t, "GET", "/v1/{second}")); err == nil || !strings.Contains(err.Error(), "duplicate HTTP match set") {
		t.Fatalf("Set.Add(duplicate) error = %v", err)
	}

	set = NewSet()
	if err := set.Add(testBinding(t, "GET", "/v1/{first}/tail")); err != nil {
		t.Fatalf("Set.Add(first conflict pattern) error = %v", err)
	}
	if err := set.Add(testBinding(t, "GET", "/v1/head/{second}")); err == nil || !strings.Contains(err.Error(), "conflicting HTTP rule") {
		t.Fatalf("Set.Add(conflict) error = %v", err)
	}
}

func testBinding(t *testing.T, method, path string) *Binding {
	t.Helper()
	template, err := httprule.Parse(path)
	if err != nil {
		t.Fatalf("httprule.Parse(%q) error = %v", path, err)
	}
	return &Binding{Method: method, Path: path, Template: template}
}

func testMethod(t *testing.T, rule *annotations.HttpRule) protoreflect.MethodDescriptor {
	t.Helper()
	options := new(descriptorpb.MethodOptions)
	proto.SetExtension(options, annotations.E_Http, rule)
	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test.proto"),
		Package:    proto.String("test"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"google/api/annotations.proto"},
		MessageType: []*descriptorpb.DescriptorProto{
			{
				Name: proto.String("Data"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("value"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
				},
			},
			{
				Name: proto.String("Request"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("name"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
					{
						Name:     proto.String("data"),
						Number:   proto.Int32(2),
						Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
						TypeName: proto.String(".test.Data"),
					},
				},
			},
			{
				Name: proto.String("Reply"),
				Field: []*descriptorpb.FieldDescriptorProto{
					{
						Name:   proto.String("result"),
						Number: proto.Int32(1),
						Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
						Type:   descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
					},
				},
			},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("API"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("Call"),
						InputType:  proto.String(".test.Request"),
						OutputType: proto.String(".test.Reply"),
						Options:    options,
					},
				},
			},
		},
	}
	files := new(protoregistry.Files)
	if err := files.RegisterFile(annotations.File_google_api_http_proto); err != nil {
		t.Fatalf("register google/api/http.proto: %v", err)
	}
	if err := files.RegisterFile(annotations.File_google_api_annotations_proto); err != nil {
		t.Fatalf("register google/api/annotations.proto: %v", err)
	}
	descriptor, err := protodesc.NewFile(file, files)
	if err != nil {
		t.Fatalf("protodesc.NewFile() error = %v", err)
	}
	return descriptor.Services().Get(0).Methods().Get(0)
}
