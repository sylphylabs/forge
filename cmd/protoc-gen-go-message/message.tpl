{{$svrType := .ServiceType}}
{{$svrName := .ServiceName}}

{{- range .Methods}}
const OperationMessage{{$svrType}}{{.OriginalName}} = "/{{$svrName}}/{{.OriginalName}}"
{{- end}}

{{- range .Methods}}
// Destination{{$svrType}}{{.OriginalName}} is the destination declared by the
// contract. It is the default for Register{{$svrType}}MessageServer, not a
// fixed value: see {{$svrType}}MessageRegisterOption.
const Destination{{$svrType}}{{.OriginalName}} = "{{.Destination}}"
{{- end}}

{{if .Deprecated}}{{$.DeprecationComment}}
{{end -}}
type {{.ServiceType}}MessageServer interface {
{{- range .Methods}}
	{{- if ne .Comment ""}}
	{{.Comment}}
	{{- end}}
	{{.Name}}(context.Context, *{{.Request}}) error
{{- end}}
}

// {{.ServiceType}}MessageRegisterOption overrides contract defaults when
// {{.ServiceType}}MessageServer is registered. The proto destination is the
// contract's default; deployments whose topic prefix or naming scheme differs
// override it here instead of editing the schema.
type {{.ServiceType}}MessageRegisterOption func(*{{.ServiceType | lowerFirst}}MessageRegisterOptions)

type {{.ServiceType | lowerFirst}}MessageRegisterOptions struct {
	prefix       string
	destinations map[string]string
}

// With{{.ServiceType}}MessageDestination replaces the destination of one
// operation. The operation is the RPC name declared in the proto file, which
// stays stable even when the Go method name is remapped.
func With{{.ServiceType}}MessageDestination(operation, destination string) {{.ServiceType}}MessageRegisterOption {
	return func(o *{{.ServiceType | lowerFirst}}MessageRegisterOptions) {
		if o.destinations == nil {
			o.destinations = make(map[string]string)
		}
		o.destinations[operation] = destination
	}
}

// With{{.ServiceType}}MessageDestinationPrefix prepends a prefix to every
// destination that With{{.ServiceType}}MessageDestination did not replace
// outright. The prefix is applied verbatim: a separator, where the broker
// needs one, belongs in the prefix.
func With{{.ServiceType}}MessageDestinationPrefix(prefix string) {{.ServiceType}}MessageRegisterOption {
	return func(o *{{.ServiceType | lowerFirst}}MessageRegisterOptions) {
		o.prefix = prefix
	}
}

func (o *{{.ServiceType | lowerFirst}}MessageRegisterOptions) resolve(operation, declared string) string {
	if replacement, ok := o.destinations[operation]; ok {
		return replacement
	}
	return o.prefix + declared
}

// Register{{.ServiceType}}MessageServer binds every subscribe-annotated method
// of the service to s. Registration must happen before the server starts; the
// first binding error is returned and no further method is bound.
func Register{{.ServiceType}}MessageServer(s *message.Server, srv {{.ServiceType}}MessageServer, opts ...{{.ServiceType}}MessageRegisterOption) error {
	o := new({{.ServiceType | lowerFirst}}MessageRegisterOptions)
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	{{- range .Methods}}
	if err := s.Handle(
		o.resolve("{{.OriginalName}}", Destination{{$svrType}}{{.OriginalName}}),
		_{{$svrType}}_{{.Name}}_Message_Handler(srv),
	); err != nil {
		return err
	}
	{{- end}}
	return nil
}

{{range .Methods}}
func _{{$svrType}}_{{.Name}}_Message_Handler(srv {{$svrType}}MessageServer) middleware.UnaryHandler {
	return func(ctx context.Context, req any) (any, error) {
		var in {{.Request}}
		if msg, ok := req.(*message.Message); ok && msg != nil {
			if err := proto.Unmarshal(msg.Body, &in); err != nil {
				return nil, err
			}
		}
		return nil, srv.{{.Name}}(ctx, &in)
	}
}
{{end}}
