package main

import (
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"

	"github.com/sylphylabs/forge/cmd/internal/generator"
)

var (
	showVersion  = flag.Bool("version", false, "print the version and exit")
	httpModeFlag = flag.String("http", "", "generate HTTP wrappers for annotated or all RPC methods")
	generateGRPC = flag.Bool("grpc", false, "generate wrappers for protoc-gen-go-grpc services")
)

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-go-middleware %s\n", generator.Release)
		return
	}
	protogen.Options{ParamFunc: flag.CommandLine.Set}.Run(func(gen *protogen.Plugin) error {
		generator.Configure(gen)
		mode, err := parseHTTPMode(*httpModeFlag)
		if err != nil {
			return err
		}
		for _, file := range gen.Files {
			if file.Generate {
				if err := validateMiddlewareIdentifiers(gen, file, mode, *generateGRPC); err != nil {
					return err
				}
			}
		}
		for _, file := range gen.Files {
			if !file.Generate {
				continue
			}
			if _, err := generateMiddlewareFile(gen, file, mode, *generateGRPC); err != nil {
				return err
			}
		}
		return nil
	})
}
