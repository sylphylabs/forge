package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
	"unicode"
	"unicode/utf8"
)

//go:embed message.tpl
var messageTemplate string

type serviceDesc struct {
	ServiceType string // OrderEvents
	ServiceName string // order.v1.OrderEvents
	Deprecated  bool
	Methods     []*methodDesc
}

// DeprecationComment is exposed to the template so the marker text has one
// definition.
func (s *serviceDesc) DeprecationComment() string { return deprecationComment }

type methodDesc struct {
	Name         string // OnOrderCreated
	OriginalName string // OnOrderCreated, as declared in the proto file
	Request      string // *OrderCreated, qualified for the generated file
	Comment      string
	Destination  string // order.created
}

var templateFuncs = template.FuncMap{"lowerFirst": lowerFirst}

func (s *serviceDesc) execute() (string, error) {
	tmpl, err := template.New("message").Funcs(templateFuncs).Parse(strings.TrimSpace(messageTemplate))
	if err != nil {
		return "", err
	}
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, s); err != nil {
		return "", err
	}
	return strings.Trim(buf.String(), "\r\n"), nil
}

// lowerFirst makes an exported generated name unexported. Protobuf identifiers
// are ASCII, but decoding the first rune keeps the result valid Go for any
// input protogen produces.
func lowerFirst(name string) string {
	if name == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(first)) + name[size:]
}
