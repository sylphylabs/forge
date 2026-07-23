package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

//go:embed httpTemplate.tpl
var httpTemplate string

type serviceDesc struct {
	ServiceType   string // Greeter
	ServiceName   string // helloworld.Greeter
	Metadata      string // api/helloworld/helloworld.proto
	Methods       []*methodDesc
	ClientMethods []*methodDesc
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

func (s *serviceDesc) execute() string {
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
	tmpl, err := template.New("http").Parse(strings.TrimSpace(httpTemplate))
	if err != nil {
		panic(err)
	}
	if err := tmpl.Execute(buf, s); err != nil {
		panic(err)
	}
	return strings.Trim(buf.String(), "\r\n")
}
