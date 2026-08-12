{{ range .Errors }}
{{ if .HasComment }}{{ .Comment }}{{ end -}}
var {{ .SentinelName }} = {{ $.ErrorsIdent }}.MustDefine({{ $.ErrorsIdent }}.{{ .KindIdent }}, {{ .Domain | printf "%q" }}, {{ .Value | printf "%q" }})
{{ end -}}
