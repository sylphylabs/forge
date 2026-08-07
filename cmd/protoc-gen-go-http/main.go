package main

import (
	"errors"
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/sylphylabs/forge/cmd/internal/generator"
)

var (
	showVersion     = flag.Bool("version", false, "print the version and exit")
	omitempty       = flag.Bool("omitempty", true, "omit HTTP output when google.api.http annotations are absent")
	omitemptyPrefix = flag.String("omitempty_prefix", "", "prefix for generated default HTTP routes")
)

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-go-http %s\n", generator.Release)
		return
	}
	protogen.Options{ParamFunc: flag.CommandLine.Set}.Run(func(gen *protogen.Plugin) error {
		generator.Configure(gen)
		if err := validateHTTPGeneration(gen.Request, *omitempty, *omitemptyPrefix); err != nil {
			return err
		}
		for _, file := range gen.Files {
			if !file.Generate {
				continue
			}
			if _, err := generateHTTPFile(gen, file, *omitempty, *omitemptyPrefix); err != nil {
				return err
			}
		}
		return nil
	})
}

func validateHTTPGeneration(request *pluginpb.CodeGeneratorRequest, omitEmpty bool, omitEmptyPrefix string) error {
	cloned := proto.Clone(request).(*pluginpb.CodeGeneratorRequest)
	probe, err := (protogen.Options{ParamFunc: func(string, string) error { return nil }}).New(cloned)
	if err != nil {
		return fmt.Errorf("go-http: initialize validation: %w", err)
	}
	for _, file := range probe.Files {
		if !file.Generate {
			continue
		}
		if _, err := generateHTTPFile(probe, file, omitEmpty, omitEmptyPrefix); err != nil {
			return err
		}
	}
	if message := probe.Response().GetError(); message != "" {
		return errors.New(message)
	}
	return nil
}
