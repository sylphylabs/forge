package main

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	errorapi "github.com/sylphylabs/forge/api/errors/v1"
)

func TestGenerateForgeErrorAnnotations(t *testing.T) {
	enumOptions := new(descriptorpb.EnumOptions)
	proto.SetExtension(enumOptions, errorapi.E_DefaultCode, int32(500))
	valueOptions := new(descriptorpb.EnumValueOptions)
	proto.SetExtension(valueOptions, errorapi.E_Code, int32(404))

	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/errors.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"sylphy/errors/v1/errors.proto"},
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/test/v1;testv1"),
		},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name:    proto.String("ErrorReason"),
			Options: enumOptions,
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("INTERNAL"), Number: proto.Int32(0)},
				{Name: proto.String("NOT_FOUND"), Number: proto.Int32(1), Options: valueOptions},
			},
		}},
	}
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(errorapi.File_sylphy_errors_v1_errors_proto),
			file,
		},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatalf("protogen.Options.New() error = %v", err)
	}
	generated, err := generateErrorFile(plugin, plugin.FilesByPath[file.GetName()])
	if err != nil {
		t.Fatalf("generateErrorFile() error = %v", err)
	}
	if generated == nil {
		t.Fatal("generateErrorFile() returned nil")
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
		`func ErrorInternal(`,
		`errors.New(500, ErrorReason_INTERNAL.String()`,
		`func ErrorNotFound(`,
		`errors.New(404, ErrorReason_NOT_FOUND.String()`,
		`github.com/sylphylabs/forge/errors`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated output is missing %q:\n%s", want, content)
		}
	}
}

func TestAnalyzeErrorFileRejectsInvalidCodes(t *testing.T) {
	tests := []struct {
		name        string
		defaultCode int32
		valueCode   *int32
		want        []string
	}{
		{
			name:        "enum default",
			defaultCode: 601,
			want:        []string{`proto "test/v1/errors.proto"`, "enum test.v1.ErrorReason", "default_code 601", "[0, 600]"},
		},
		{
			name:        "enum value",
			defaultCode: 500,
			valueCode:   int32Pointer(-1),
			want:        []string{`proto "test/v1/errors.proto"`, "enum value test.v1.NOT_FOUND", "code -1", "[0, 600]"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugin, file := newErrorPlugin(t, test.defaultCode, test.valueCode)
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("analyzeErrorFile() panicked: %v", recovered)
				}
			}()
			_, err := analyzeErrorFile(plugin.FilesByPath[file.GetName()])
			if err == nil {
				t.Fatal("analyzeErrorFile() error = nil")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func newErrorPlugin(t *testing.T, defaultCode int32, valueCode *int32) (*protogen.Plugin, *descriptorpb.FileDescriptorProto) {
	t.Helper()
	enumOptions := new(descriptorpb.EnumOptions)
	proto.SetExtension(enumOptions, errorapi.E_DefaultCode, defaultCode)
	valueOptions := new(descriptorpb.EnumValueOptions)
	if valueCode != nil {
		proto.SetExtension(valueOptions, errorapi.E_Code, *valueCode)
	}
	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/errors.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"sylphy/errors/v1/errors.proto"},
		Options:    &descriptorpb.FileOptions{GoPackage: proto.String("example.com/test/v1;testv1")},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name:    proto.String("ErrorReason"),
			Options: enumOptions,
			Value: []*descriptorpb.EnumValueDescriptorProto{
				{Name: proto.String("UNSPECIFIED"), Number: proto.Int32(0)},
				{Name: proto.String("NOT_FOUND"), Number: proto.Int32(1), Options: valueOptions},
			},
		}},
	}
	request := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{file.GetName()},
		ProtoFile: []*descriptorpb.FileDescriptorProto{
			protodesc.ToFileDescriptorProto(descriptorpb.File_google_protobuf_descriptor_proto),
			protodesc.ToFileDescriptorProto(errorapi.File_sylphy_errors_v1_errors_proto),
			file,
		},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatalf("protogen.Options.New() error = %v", err)
	}
	return plugin, file
}

func int32Pointer(value int32) *int32 {
	return &value
}

func Test_case2Camel(t *testing.T) {
	type args struct {
		name string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "snake1",
			args: args{"SYSTEM_ERROR"},
			want: "SystemError",
		},
		{
			name: "snake2",
			args: args{"System_Error"},
			want: "SystemError",
		},
		{
			name: "snake3",
			args: args{"system_error"},
			want: "SystemError",
		},
		{
			name: "snake4",
			args: args{"System_error"},
			want: "SystemError",
		},
		{
			name: "upper1",
			args: args{"UNKNOWN"},
			want: "Unknown",
		},
		{
			name: "camel1",
			args: args{"SystemError"},
			want: "SystemError",
		},
		{
			name: "camel2",
			args: args{"systemError"},
			want: "SystemError",
		},
		{
			name: "lower1",
			args: args{"system"},
			want: "System",
		},
		{
			name: "empty segments",
			args: args{"SYSTEM__ERROR_"},
			want: "SystemError",
		},
		{
			name: "number suffix",
			args: args{"ERROR_404"},
			want: "Error404",
		},
		{
			name: "empty",
			args: args{""},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := case2Camel(tt.args.name); got != tt.want {
				t.Errorf("case2Camel() = %v, want %v", got, tt.want)
			}
		})
	}
}
