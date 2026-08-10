package main

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"

	messageapi "github.com/sylphylabs/forge/api/message/v1"
	"github.com/sylphylabs/forge/cmd/internal/generator"
)

const (
	contextPackage          = protogen.GoImportPath("context")
	fmtPackage              = protogen.GoImportPath("fmt")
	stringsPackage          = protogen.GoImportPath("strings")
	transportMessagePackage = protogen.GoImportPath("github.com/sylphylabs/forge/transport/message")
	middlewarePackage       = protogen.GoImportPath("github.com/sylphylabs/forge/middleware")
	protoPackage            = protogen.GoImportPath("google.golang.org/protobuf/proto")
)

// binding is one analyzed subscribe-annotated method. Analysis is deliberately
// separate from rendering so a whole file can be validated before any import is
// recorded in a generated file.
type binding struct {
	method      *protogen.Method
	destination string
}

type serviceFacts struct {
	service  *protogen.Service
	bindings []*binding
}

type messageFile struct {
	services []*serviceFacts
}

// analyzeMessageFile collects every subscribe-annotated method in the file.
func analyzeMessageFile(file *protogen.File) (*messageFile, error) {
	facts := new(messageFile)
	// Destinations are checked across the whole file, not per service. Two
	// services registered on one server would otherwise both bind the same
	// destination, and the second Handle shadows the first without an error.
	declaredBy := make(map[string]string)
	for _, service := range file.Services {
		analyzed, err := analyzeService(file, service, declaredBy)
		if err != nil {
			return nil, err
		}
		if analyzed != nil {
			facts.services = append(facts.services, analyzed)
		}
	}
	return facts, nil
}

func analyzeService(file *protogen.File, service *protogen.Service, declaredBy map[string]string) (*serviceFacts, error) {
	facts := &serviceFacts{service: service}
	for _, method := range service.Methods {
		subscription, ok := subscriptionOf(method)
		if !ok {
			continue
		}
		destination := strings.TrimSpace(subscription.GetDestination())
		if destination == "" {
			return nil, fmt.Errorf(
				"proto %q RPC %s: (sylphy.message.v1.subscribe) requires a non-empty destination",
				file.Desc.Path(), method.Desc.FullName(),
			)
		}
		if method.Desc.IsStreamingClient() || method.Desc.IsStreamingServer() {
			return nil, fmt.Errorf(
				"proto %q RPC %s: (sylphy.message.v1.subscribe) does not support streaming methods",
				file.Desc.Path(), method.Desc.FullName(),
			)
		}
		if previous, duplicated := declaredBy[destination]; duplicated {
			return nil, fmt.Errorf(
				"proto %q RPC %s: destination %q is already bound by %s",
				file.Desc.Path(), method.Desc.FullName(), destination, previous,
			)
		}
		declaredBy[destination] = string(method.Desc.FullName())
		facts.bindings = append(facts.bindings, &binding{method: method, destination: destination})
	}
	if len(facts.bindings) == 0 {
		return nil, nil
	}
	return facts, nil
}

// subscriptionOf reads the sylphy.message.v1.subscribe method option. The
// second result reports whether the method declares the option at all, so a
// present-but-empty destination stays distinguishable from no annotation.
func subscriptionOf(method *protogen.Method) (*messageapi.Subscription, bool) {
	options := method.Desc.Options()
	if options == nil || !proto.HasExtension(options, messageapi.E_Subscribe) {
		return nil, false
	}
	subscription, ok := proto.GetExtension(options, messageapi.E_Subscribe).(*messageapi.Subscription)
	if !ok || subscription == nil {
		return nil, false
	}
	return subscription, true
}

func buildServiceDesc(g *protogen.GeneratedFile, _ *protogen.File, facts *serviceFacts) *serviceDesc {
	sd := &serviceDesc{
		ServiceType: facts.service.GoName,
		ServiceName: string(facts.service.Desc.FullName()),
		Deprecated:  facts.service.Desc.Options().(*descriptorpb.ServiceOptions).GetDeprecated(),
	}
	for _, b := range facts.bindings {
		sd.Methods = append(sd.Methods, buildMethodDesc(g, b))
	}
	return sd
}

func buildMethodDesc(g *protogen.GeneratedFile, b *binding) *methodDesc {
	method := b.method
	comment := method.Comments.Leading.String() + method.Comments.Trailing.String()
	if comment != "" {
		comment = "// " + method.GoName + strings.TrimPrefix(strings.TrimSuffix(comment, "\n"), "//")
	}
	if method.Desc.Options().(*descriptorpb.MethodOptions).GetDeprecated() {
		if comment != "" {
			comment += "\n"
		}
		comment += deprecationComment
	}
	return &methodDesc{
		Name:         method.GoName,
		OriginalName: string(method.Desc.Name()),
		Request:      g.QualifiedGoIdent(method.Input.GoIdent),
		Comment:      comment,
		Destination:  b.destination,
	}
}

// generateMessageFile analyzes and emits one file for focused tests.
func generateMessageFile(gen *protogen.Plugin, file *protogen.File) (*protogen.GeneratedFile, error) {
	facts, err := analyzeMessageFile(file)
	if err != nil {
		return nil, err
	}
	return emitMessageFile(gen, file, facts)
}

func emitMessageFile(gen *protogen.Plugin, file *protogen.File, facts *messageFile) (*protogen.GeneratedFile, error) {
	if facts == nil || len(facts.services) == 0 {
		return nil, nil
	}
	filename := file.GeneratedFilenamePrefix + "_message.pb.go"
	g := gen.NewGeneratedFile(filename, file.GoImportPath)
	g.P("// Code generated by protoc-gen-go-message. DO NOT EDIT.")
	g.P("// versions:")
	g.P(fmt.Sprintf("// - protoc-gen-go-message %s", generator.Release))
	g.P("// - protoc                ", generator.ProtocVersion(gen))
	if file.Proto.GetOptions().GetDeprecated() {
		g.P("// ", file.Desc.Path(), " is a deprecated file.")
	} else {
		g.P("// source: ", file.Desc.Path())
	}
	g.P()
	g.P("package ", file.GoPackageName)
	g.P()
	g.P("// This is a compile-time assertion to ensure that this generated file")
	g.P("// is compatible with the Forge package it is being compiled against.")
	g.P("var _ = new(", contextPackage.Ident("Context"), ")")
	g.P("var _ = new(", transportMessagePackage.Ident("Message"), ")")
	g.P("var _ ", middlewarePackage.Ident("UnaryHandler"))
	g.P("var _ = ", protoPackage.Ident("Unmarshal"))
	g.P("var _ = ", fmtPackage.Ident("Errorf"))
	g.P("var _ = ", stringsPackage.Ident("TrimSpace"))
	g.P()
	for _, service := range facts.services {
		source, err := buildServiceDesc(g, file, service).execute()
		if err != nil {
			return nil, fmt.Errorf("proto %q: render message bindings: %w", file.Desc.Path(), err)
		}
		g.P(source)
	}
	return g, nil
}

const deprecationComment = "// Deprecated: Do not use."
