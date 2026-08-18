package main

import (
	"fmt"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/sylphylabs/forge/cmd/internal/generator"
	openapigen "github.com/sylphylabs/forge/cmd/internal/openapi/generator"
)

func s(v string) *string { return &v }
func b(v bool) *bool     { return &v }
func i(v int) *int       { return &v }

func main() {
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	fds := &descriptorpb.FileDescriptorSet{}
	if unmarshalErr := proto.Unmarshal(raw, fds); unmarshalErr != nil {
		panic(unmarshalErr)
	}
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{"qp.proto"},
		ProtoFile:      fds.File,
		CompilerVersion: &pluginpb.Version{
			Major: proto.Int32(3), Minor: proto.Int32(21), Patch: proto.Int32(0),
		},
	}
	plugin, err := protogen.Options{}.New(req)
	if err != nil {
		panic(err)
	}
	generator.Configure(plugin)
	conf := openapigen.Configuration{
		OpenAPIVersion:  s("3.2.0"),
		Version:         s("0.0.1"),
		Title:           s("Proof"),
		Description:     s(""),
		Naming:          s("json"),
		FQSchemaNaming:  b(false),
		CircularDepth:   i(2),
		DefaultResponse: b(true),
		ErrorSchemaName: s("ForgeProblem"),
		OutputMode:      s("merged"),
	}
	var files []*protogen.File
	for _, f := range plugin.Files {
		if f.Generate {
			files = append(files, f)
		}
	}
	out := plugin.NewGeneratedFile("openapi.yaml", "")
	if err := openapigen.NewOpenAPIv3Generator(plugin, conf, files).Run(out); err != nil {
		panic(err)
	}
	resp := plugin.Response()
	if resp.GetError() != "" {
		panic(resp.GetError())
	}
	for _, f := range resp.File {
		fmt.Print(f.GetContent())
	}
}
