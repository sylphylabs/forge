package main

import (
	"strings"
	"testing"

	errorsv1 "github.com/openkratos/api/errors/v1"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func TestGenerateOpenKratosErrorAnnotations(t *testing.T) {
	enumOptions := new(descriptorpb.EnumOptions)
	proto.SetExtension(enumOptions, errorsv1.E_DefaultCode, int32(500))
	valueOptions := new(descriptorpb.EnumValueOptions)
	proto.SetExtension(valueOptions, errorsv1.E_Code, int32(404))

	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/errors.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"openkratos/errors/v1/errors.proto"},
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
			protodesc.ToFileDescriptorProto(errorsv1.File_openkratos_errors_v1_errors_proto),
			file,
		},
	}
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		t.Fatalf("protogen.Options.New() error = %v", err)
	}
	generated := generateFile(plugin, plugin.FilesByPath[file.GetName()])
	if generated == nil {
		t.Fatal("generateFile() returned nil")
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
		`github.com/openkratos/kratos/errors`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated output is missing %q:\n%s", want, content)
		}
	}
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
