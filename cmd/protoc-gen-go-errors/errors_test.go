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

// enumSpec describes the error enum a test wants generated.
type enumSpec struct {
	name        string
	defaultKind *errorapi.Kind
	values      []valueSpec
}

type valueSpec struct {
	name   string
	number int32
	kind   *errorapi.Kind
}

func kindPointer(k errorapi.Kind) *errorapi.Kind { return &k }

func newPlugin(t *testing.T, spec enumSpec) (*protogen.Plugin, *descriptorpb.FileDescriptorProto) {
	t.Helper()
	enumOptions := new(descriptorpb.EnumOptions)
	if spec.defaultKind != nil {
		proto.SetExtension(enumOptions, errorapi.E_DefaultKind, *spec.defaultKind)
	}
	values := make([]*descriptorpb.EnumValueDescriptorProto, 0, len(spec.values))
	for _, v := range spec.values {
		valueOptions := new(descriptorpb.EnumValueOptions)
		if v.kind != nil {
			proto.SetExtension(valueOptions, errorapi.E_Kind, *v.kind)
		}
		values = append(values, &descriptorpb.EnumValueDescriptorProto{
			Name:    proto.String(v.name),
			Number:  proto.Int32(v.number),
			Options: valueOptions,
		})
	}
	file := &descriptorpb.FileDescriptorProto{
		Name:       proto.String("test/v1/errors.proto"),
		Package:    proto.String("test.v1"),
		Syntax:     proto.String("proto3"),
		Dependency: []string{"sylphy/errors/v1/errors.proto"},
		Options:    &descriptorpb.FileOptions{GoPackage: proto.String("example.com/test/v1;testv1")},
		EnumType: []*descriptorpb.EnumDescriptorProto{{
			Name:    proto.String(spec.name),
			Options: enumOptions,
			Value:   values,
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

func TestGenerateSentinels(t *testing.T) {
	plugin, file := newPlugin(t, enumSpec{
		name:        "FailureReason",
		defaultKind: kindPointer(errorapi.Kind_KIND_INTERNAL),
		values: []valueSpec{
			{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
			{name: "FAILURE_REASON_NOT_FOUND", number: 1, kind: kindPointer(errorapi.Kind_KIND_NOT_FOUND)},
			{name: "FAILURE_REASON_BACKEND_DOWN", number: 2},
		},
	})
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
	content := response.File[0].GetContent()
	for _, want := range []string{
		// A sentinel value, not a constructor function.
		`var ErrNotFound = errors.MustDefine(`,
		`errors.KindNotFound`,
		// The domain is the proto package, so reasons cannot collide globally.
		`"test.v1"`,
		// The reason is the enum value name as a literal: a package-level var
		// initializes before the init() that registers the enum descriptor, so
		// calling String() here would dereference an unregistered descriptor.
		`"FAILURE_REASON_NOT_FOUND"`,
		// A value without its own kind inherits default_kind.
		`var ErrBackendDown = errors.MustDefine(`,
		`errors.KindInternal`,
		`github.com/sylphylabs/forge/errors`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated output is missing %q:\n%s", want, content)
		}
	}
	// The zero value names the absence of a failure and must not become one.
	if strings.Contains(content, "ErrUnspecified") {
		t.Errorf("generated a sentinel for the zero value:\n%s", content)
	}
}

// A kind on any value is enough to opt an enum in, so default_kind is optional
// for the common case where every error is internal unless stated otherwise.
func TestGenerateWithoutDefaultKind(t *testing.T) {
	plugin, file := newPlugin(t, enumSpec{
		name: "FailureReason",
		values: []valueSpec{
			{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
			{name: "FAILURE_REASON_NOT_FOUND", number: 1, kind: kindPointer(errorapi.Kind_KIND_NOT_FOUND)},
		},
	})
	if _, err := generateErrorFile(plugin, plugin.FilesByPath[file.GetName()]); err != nil {
		t.Fatalf("generateErrorFile() error = %v", err)
	}
	content := plugin.Response().File[0].GetContent()
	if !strings.Contains(content, "var ErrNotFound = errors.MustDefine(") {
		t.Errorf("generated output is missing the sentinel:\n%s", content)
	}
}

// An enum that declares nothing is an ordinary enum and must be left alone.
func TestIgnoresPlainEnum(t *testing.T) {
	plugin, file := newPlugin(t, enumSpec{
		name: "Color",
		values: []valueSpec{
			{name: "COLOR_UNSPECIFIED", number: 0},
			{name: "COLOR_RED", number: 1},
		},
	})
	generated, err := generateErrorFile(plugin, plugin.FilesByPath[file.GetName()])
	if err != nil {
		t.Fatalf("generateErrorFile() error = %v", err)
	}
	if generated != nil {
		t.Error("generated a file for an enum that declares no kinds")
	}
}

// A reason travels to other services and is matched there as a literal, so an
// inconsistent one is a wire-format defect. It must fail the build.
func TestRejectsInvalidDeclarations(t *testing.T) {
	tests := []struct {
		name string
		spec enumSpec
		want []string
	}{
		{
			name: "reason not prefixed by enum name",
			spec: enumSpec{
				name:        "FailureReason",
				defaultKind: kindPointer(errorapi.Kind_KIND_INTERNAL),
				values: []valueSpec{
					{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
					{name: "NOT_FOUND", number: 1},
				},
			},
			want: []string{"test.v1.NOT_FOUND", `prefixed with "FAILURE_REASON_"`},
		},
		{
			name: "reason not screaming snake case",
			spec: enumSpec{
				name:        "FailureReason",
				defaultKind: kindPointer(errorapi.Kind_KIND_INTERNAL),
				values: []valueSpec{
					{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
					{name: "FailureReason_notFound", number: 1},
				},
			},
			want: []string{"SCREAMING_SNAKE_CASE"},
		},
		{
			name: "kind on the zero value",
			spec: enumSpec{
				name: "FailureReason",
				values: []valueSpec{
					{name: "FAILURE_REASON_UNSPECIFIED", number: 0, kind: kindPointer(errorapi.Kind_KIND_NOT_FOUND)},
				},
			},
			want: []string{"zero value must not declare a kind"},
		},
		{
			name: "unspecified kind on a value",
			spec: enumSpec{
				name: "FailureReason",
				values: []valueSpec{
					{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
					{name: "FAILURE_REASON_NOT_FOUND", number: 1, kind: kindPointer(errorapi.Kind_KIND_UNSPECIFIED)},
				},
			},
			want: []string{"must not be KIND_UNSPECIFIED"},
		},
		{
			name: "reason is only the prefix",
			spec: enumSpec{
				name:        "FailureReason",
				defaultKind: kindPointer(errorapi.Kind_KIND_INTERNAL),
				values: []valueSpec{
					{name: "FAILURE_REASON_UNSPECIFIED", number: 0},
					{name: "FAILURE_REASON_", number: 1},
				},
			},
			want: []string{"must name a failure after"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plugin, file := newPlugin(t, test.spec)
			_, err := analyzeErrorFile(plugin.FilesByPath[file.GetName()])
			if err == nil {
				t.Fatal("analyzeErrorFile() error = nil, want an error")
			}
			for _, want := range test.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q is missing %q", err, want)
				}
			}
		})
	}
}

func TestScreamingSnake(t *testing.T) {
	tests := []struct{ in, want string }{
		{"FailureReason", "FAILURE_REASON"},
		{"Color", "COLOR"},
		{"HTTPError", "H_T_T_P_ERROR"},
	}
	for _, tt := range tests {
		if got := screamingSnake(tt.in); got != tt.want {
			t.Errorf("screamingSnake(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func Test_case2Camel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "snake1", in: "SYSTEM_ERROR", want: "SystemError"},
		{name: "snake2", in: "System_Error", want: "SystemError"},
		{name: "snake3", in: "system_error", want: "SystemError"},
		{name: "snake4", in: "System_error", want: "SystemError"},
		{name: "upper1", in: "UNKNOWN", want: "Unknown"},
		{name: "camel1", in: "SystemError", want: "SystemError"},
		{name: "camel2", in: "systemError", want: "SystemError"},
		{name: "lower1", in: "system", want: "System"},
		{name: "empty segments", in: "SYSTEM__ERROR_", want: "SystemError"},
		{name: "number suffix", in: "ERROR_404", want: "Error404"},
		{name: "empty", in: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := case2Camel(tt.in); got != tt.want {
				t.Errorf("case2Camel() = %v, want %v", got, tt.want)
			}
		})
	}
}
