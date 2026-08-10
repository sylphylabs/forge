package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

//go:embed http.tpl
var httpTemplate string

// The default aliases protogen picks for the support packages when nothing
// competes for the names. They are the fallbacks used when a service is
// rendered without a generated file to resolve imports against.
const (
	defaultHTTPIdent        = "http"
	defaultContextIdent     = "context"
	defaultTranscodingIdent = "transcoding"
)

type serviceDesc struct {
	ServiceType   string // Greeter
	ServiceName   string // helloworld.Greeter
	Metadata      string // api/helloworld/helloworld.proto
	MethodSet     int
	Methods       []*methodDesc
	ClientMethods []*methodDesc
	// HTTPIdent, ContextIdent and TranscodingIdent are how the HTTP transport,
	// context and transcoding packages are referred to in the generated file.
	// protogen chooses the name, so the template must not assume one: a message
	// package that claims "http", "context" or "transcoding" first forces
	// protogen to suffix the support package's alias, and a template that
	// hardcoded the bare name would emit references to the wrong package.
	HTTPIdent        string
	ContextIdent     string
	TranscodingIdent string
}

type methodDesc struct {
	// method
	Name         string
	OriginalName string // The parsed original name
	Num          int
	Request      string
	Reply        string
	Comment      string
	// http_rule
	Path                 string
	PathTemplate         string
	PathFields           []string
	Method               string
	HasVars              bool
	HasBody              bool
	BodyField            string
	BodyQueryName        string
	BodyGetter           string
	BodyType             string
	BodyAssignment       string
	BodyHTTPBody         bool
	BodyMessage          bool
	BodyProtoJSON        bool
	ResponseBodyField    string
	ResponseBodyGetter   string
	ResponseBodyType     string
	ResponseAssignment   string
	ResponseBodyHTTPBody bool
	ReplyHTTPBody        bool
	ClientStreaming      bool
	ServerStreaming      bool
	UnspecifiedMethod    bool
	UnboundPathWildcard  bool
	serveMuxPattern      string
	matchKey             string
}

// bindScope pairs a method with its service so a sub-template can reach both.
// Invoking a sub-template rebinds "$" to its argument, so the service-scoped
// package aliases have to travel alongside the method rather than through "$".
type bindScope struct {
	Service *serviceDesc
	Method  *methodDesc
}

func (s *serviceDesc) execute() string {
	// Callers that render a service without a generated file (unit tests, and any
	// caller that does not resolve imports) get the aliases protogen picks when
	// nothing competes for them, which keeps the rendered output readable.
	if s.HTTPIdent == "" {
		s.HTTPIdent = defaultHTTPIdent
	}
	if s.ContextIdent == "" {
		s.ContextIdent = defaultContextIdent
	}
	if s.TranscodingIdent == "" {
		s.TranscodingIdent = defaultTranscodingIdent
	}
	if len(s.ClientMethods) == 0 {
		seen := make(map[string]struct{})
		for _, method := range s.Methods {
			if _, ok := seen[method.Name]; ok {
				continue
			}
			seen[method.Name] = struct{}{}
			s.ClientMethods = append(s.ClientMethods, method)
		}
	}
	buf := new(bytes.Buffer)
	funcs := template.FuncMap{
		"bind": func(service *serviceDesc, method *methodDesc) bindScope {
			return bindScope{Service: service, Method: method}
		},
	}
	tmpl, err := template.New("http").Funcs(funcs).Parse(strings.TrimSpace(httpTemplate))
	if err != nil {
		panic(err)
	}
	if err := tmpl.Execute(buf, s); err != nil {
		panic(err)
	}
	return strings.Trim(buf.String(), "\r\n")
}
