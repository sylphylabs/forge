package main

import (
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

var (
	showVersion     = flag.Bool("version", false, "print the version and exit")
	omitempty       = flag.Bool("http_omitempty", true, "omit HTTP output if google.api is empty")
	omitemptyPrefix = flag.String("http_omitempty_prefix", "", "prefix for generated default HTTP routes")
	generateGRPC    = flag.Bool("grpc", false, "generate middleware wrappers for protoc-gen-go-grpc services")
)

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-go-openkratos %v\n", release)
		return
	}
	protogen.Options{
		ParamFunc: flag.CommandLine.Set,
	}.Run(func(gen *protogen.Plugin) error {
		gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL | pluginpb.CodeGeneratorResponse_FEATURE_SUPPORTS_EDITIONS)
		gen.SupportedEditionsMinimum = descriptorpb.Edition_EDITION_PROTO2
		gen.SupportedEditionsMaximum = descriptorpb.Edition_EDITION_2024
		for _, f := range gen.Files {
			if !f.Generate {
				continue
			}
			generateErrorFile(gen, f)
			httpFile, err := generateHTTPFile(gen, f, *omitempty, *omitemptyPrefix)
			if err != nil {
				return err
			}
			if _, err := generateMiddlewareFile(gen, f, httpFile != nil, *generateGRPC, *omitempty); err != nil {
				return err
			}
		}
		return nil
	})
}
