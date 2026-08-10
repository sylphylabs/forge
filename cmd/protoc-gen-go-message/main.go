package main

import (
	"flag"
	"fmt"

	"google.golang.org/protobuf/compiler/protogen"

	"github.com/sylphylabs/forge/cmd/internal/generator"
)

var showVersion = flag.Bool("version", false, "print the version and exit")

func main() {
	flag.Parse()
	if *showVersion {
		fmt.Printf("protoc-gen-go-message %s\n", generator.Release)
		return
	}
	protogen.Options{ParamFunc: flag.CommandLine.Set}.Run(func(gen *protogen.Plugin) error {
		generator.Configure(gen)
		files := make(map[*protogen.File]*messageFile)
		for _, file := range gen.Files {
			if !file.Generate {
				continue
			}
			facts, err := analyzeMessageFile(file)
			if err != nil {
				return err
			}
			files[file] = facts
		}
		for _, file := range gen.Files {
			if facts := files[file]; facts != nil {
				if _, err := emitMessageFile(gen, file, facts); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
