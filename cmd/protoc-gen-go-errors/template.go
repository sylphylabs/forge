package main

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed errors.tpl
var errorsTemplate string

type errorInfo struct {
	Name       string
	Value      string
	HTTPCode   int
	CamelValue string
	Comment    string
	HasComment bool
}

type errorWrapper struct {
	Errors []*errorInfo
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
