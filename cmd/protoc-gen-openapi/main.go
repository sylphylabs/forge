package main

import (
	"flag"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"

	"github.com/sylphylabs/forge/cmd/internal/generator"
	openapigen "github.com/sylphylabs/forge/cmd/internal/openapi/generator"
)

const (
	defaultOpenAPIVersion  = "3.2.0"
	defaultErrorSchemaName = "sylphy.errors.v1.Status"
)

var flags flag.FlagSet

func main() {
	conf := openapigen.Configuration{
		OpenAPIVersion:  flags.String("openapi_version", defaultOpenAPIVersion, "OpenAPI specification version to emit"),
		Version:         flags.String("version", "0.0.1", "API version number text, e.g. 1.2.3"),
		Title:           flags.String("title", "", "API title"),
		Description:     flags.String("description", "", "API description"),
		Naming:          flags.String("naming", "json", "naming convention. Use proto for names from proto files"),
		FQSchemaNaming:  flags.Bool("fq_schema_naming", false, "prefix schema names with the proto package"),
		EnumType:        flags.String("enum_type", "integer", "enum serialization. Use string for string-based serialization"),
		CircularDepth:   flags.Int("depth", 2, "query-parameter recursion depth for circular messages"),
		DefaultResponse: flags.Bool("default_response", true, "add an Forge default error response"),
		ErrorSchemaName: flags.String("error_schema_name", defaultErrorSchemaName, "Forge error schema component name"),
		OutputMode:      flags.String("output_mode", "merged", "output mode: merged or source_relative"),
	}

	opts := protogen.Options{
		ParamFunc: flags.Set,
	}

	opts.Run(func(plugin *protogen.Plugin) error {
		generator.Configure(plugin)
		return generateOpenAPI(plugin, conf)
	})
}

func generateOpenAPI(plugin *protogen.Plugin, conf openapigen.Configuration) error {
	if *conf.OutputMode == "source_relative" {
		for _, file := range plugin.Files {
			if !file.Generate {
				continue
			}
			outfileName := strings.TrimSuffix(file.Desc.Path(), filepath.Ext(file.Desc.Path())) + ".openapi.yaml"
			outputFile := plugin.NewGeneratedFile(outfileName, "")
			if err := openapigen.NewOpenAPIv3Generator(plugin, conf, []*protogen.File{file}).Run(outputFile); err != nil {
				return err
			}
		}
		return nil
	}

	outputFile := plugin.NewGeneratedFile("openapi.yaml", "")
	return openapigen.NewOpenAPIv3Generator(plugin, conf, plugin.Files).Run(outputFile)
}
