package main

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed errors.tpl
var errorsTemplate string

type errorInfo struct {
	// Enum is the Go identifier of the generated enum type.
	Enum string
	// Value is the Protobuf enum value name, used as the reason.
	Value string
	// SentinelName is the Go identifier of the generated sentinel variable.
	SentinelName string
	// KindIdent is the Kind constant in the errors package, without its package
	// qualifier.
	KindIdent string
	// Domain namespaces the reason. It is the Protobuf package.
	Domain     string
	Comment    string
	HasComment bool
}

type errorWrapper struct {
	Errors []*errorInfo
	// ErrorsIdent is how the errors package is referred to in the generated
	// file. protogen chooses the name, so the template must not assume one.
	ErrorsIdent string
}

func (e *errorWrapper) execute() (string, error) {
	buf := new(bytes.Buffer)
	tmpl, err := template.New("errors").Parse(errorsTemplate)
	if err != nil {
		return "", err
	}
	if err := tmpl.Execute(buf, e); err != nil {
		return "", err
	}
	return buf.String(), nil
}
