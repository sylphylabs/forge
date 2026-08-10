{{$svrType := .ServiceType}}
{{$svrName := .ServiceName}}

{{- range .Methods}}
// Destination{{$svrType}}{{.OriginalName}} is the destination declared by the
// contract. It is the default for Register{{$svrType}}MessageServer, not a
// fixed value: see {{$svrType}}MessageRegisterOption.
//
// It is also the key middleware matches on, because a delivered message reports
// its destination as the operation in flight. There is deliberately no
// "/{{$svrName}}/{{.OriginalName}}" constant: that shape is what HTTP and gRPC
// report, and a matcher built from it would never fire here.
const Destination{{$svrType}}{{.OriginalName}} = {{.Destination | printf "%q"}}
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

// {{.ServiceType | lowerFirst}}MessageOperations lists the operations this
// contract declares, so that an override naming something else is reported
// rather than silently ignored.
var {{.ServiceType | lowerFirst}}MessageOperations = map[string]struct{}{
{{- range .Methods}}
	"{{.OriginalName}}": {},
{{- end}}
}

// Register{{.ServiceType}}MessageServer binds every subscribe-annotated method
// of the service to s. Registration must happen before the server starts.
//
// Every destination is resolved and checked before the first binding, so a
// rejected registration binds nothing at all. Binding as it went would leave a
// server that returns an error and still starts, consuming part of its contract
// while the rest is silently absent.
func Register{{.ServiceType}}MessageServer(s *message.Server, srv {{.ServiceType}}MessageServer, opts ...{{.ServiceType}}MessageRegisterOption) error {
	o := new({{.ServiceType | lowerFirst}}MessageRegisterOptions)
	for _, opt := range opts {
		if opt != nil {
			opt(o)
		}
	}
	for operation := range o.destinations {
		if _, ok := {{.ServiceType | lowerFirst}}MessageOperations[operation]; !ok {
			return fmt.Errorf("{{.ServiceName}}: override names unknown operation %q", operation)
		}
	}

	type binding struct {
		destination string
		handler     middleware.UnaryHandler
	}
	bindings := []binding{
	{{- range .Methods}}
		{
			destination: o.resolve("{{.OriginalName}}", Destination{{$svrType}}{{.OriginalName}}),
			handler:     _{{$svrType}}_{{.Name}}_Message_Handler(srv),
		},
	{{- end}}
	}

	claimed := make(map[string]struct{}, len(bindings))
	for _, b := range bindings {
		if strings.TrimSpace(b.destination) == "" {
			return fmt.Errorf("{{.ServiceName}}: empty destination after resolution")
		}
		if _, dup := claimed[b.destination]; dup {
			// The second Handle would shadow the first, so the contract would
			// be half-consumed with no error at delivery time.
			return fmt.Errorf("{{.ServiceName}}: two operations resolve to destination %q", b.destination)
		}
		claimed[b.destination] = struct{}{}
	}

	for _, b := range bindings {
		if err := s.Handle(b.destination, b.handler); err != nil {
			return err
		}
	}
	return nil
}

{{range .Methods}}
func _{{$svrType}}_{{.Name}}_Message_Handler(srv {{$svrType}}MessageServer) middleware.UnaryHandler {
	return func(ctx context.Context, req any) (any, error) {
		msg, ok := req.(*message.Message)
		if !ok || msg == nil {
			// The request is always the delivered envelope. Anything else means
			// this handler was mounted somewhere other than a message server,
			// and decoding an empty request would hand business code a
			// plausible-looking event that never arrived.
			return nil, fmt.Errorf("{{$svrName}}: handler wants a *message.Message, got %T", req)
		}
		var in {{.Request}}
		if err := proto.Unmarshal(msg.Body, &in); err != nil {
			return nil, err
		}
		return nil, srv.{{.Name}}(ctx, &in)
	}
}
{{end}}
