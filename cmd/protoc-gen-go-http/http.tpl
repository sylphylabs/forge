{{$svrType := .ServiceType}}
{{$svrName := .ServiceName}}

{{- range .ClientMethods}}
const Operation{{$svrType}}{{.OriginalName}} = "/{{$svrName}}/{{.OriginalName}}"
{{- end}}

{{- range .ClientMethods}}
{{- if and (not .UnspecifiedMethod) (not .UnboundPathWildcard)}}
var _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path = {{$.TranscodingIdent}}.MustCompilePath("{{.PathTemplate}}", new({{.Request}}){{if .HasBody}}{{if and (ne .BodyField "*") (ne .BodyField "")}}, {{$.TranscodingIdent}}.WithQueryParams(), {{$.TranscodingIdent}}.WithOmitFields("{{.BodyQueryName}}"){{end}}{{else}}, {{$.TranscodingIdent}}.WithQueryParams(){{end}})
{{- end}}
{{- end}}

const _{{.ServiceType}}HTTPMethodSet = {{.MethodSet}}

type {{.ServiceType}}HTTPServer interface {
{{- range .ClientMethods}}
	{{- if ne .Comment ""}}
	{{.Comment}}
	{{- end}}
	{{- if .ClientStreaming}}
	{{.Name}}({{$svrType}}_{{.Name}}HTTPServer) error
	{{- else if .ServerStreaming}}
	{{.Name}}(*{{.Request}}, {{$svrType}}_{{.Name}}HTTPServer) error
	{{- else}}
	{{.Name}}({{$.ContextIdent}}.Context, *{{.Request}}) (*{{.Reply}}, error)
	{{- end}}
{{- end}}
}

func Register{{.ServiceType}}HTTPServer(s *{{$.HTTPIdent}}.Server, srv {{.ServiceType}}HTTPServer) {
	r := s.Route("/")
	{{- range .Methods}}
	{{- if .ClientStreaming}}
	r.Handle("GET", "{{.Path}}", _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(srv))
	{{- else}}
	r.Handle("{{.Method}}", "{{.Path}}", _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(srv))
	{{- end}}
	{{- end}}
}

{{range .ClientMethods}}
{{- if or .ClientStreaming .ServerStreaming}}
type {{$svrType}}_{{.Name}}HTTPServer interface {
	{{- if .ServerStreaming}}
	Send(*{{.Reply}}) error
	{{- end}}
	{{- if .ClientStreaming}}
	Recv() (*{{.Request}}, error)
	{{- end}}
	{{- if and .ClientStreaming (not .ServerStreaming)}}
	SendAndClose(*{{.Reply}}) error
	{{- end}}
	{{$.HTTPIdent}}.ServerStream
}

type _{{$svrType}}_{{.Name}}HTTPServer struct {
	{{$.HTTPIdent}}.ServerStream
}

// {{$svrType}}_{{.Name}}HTTPClientStream is the client half of the stream.
//
// It is declared in HTTP terms rather than reusing the gRPC stream interface:
// the two transports carry metadata differently, and naming gRPC's here would
// require every HTTP stream to speak metadata.MD.
type {{$svrType}}_{{.Name}}HTTPClientStream interface {
	{{- if .ServerStreaming}}
	Recv() (*{{.Reply}}, error)
	{{- end}}
	{{- if .ClientStreaming}}
	Send(*{{.Request}}) error
	{{- end}}
	{{- if and .ClientStreaming (not .ServerStreaming)}}
	CloseAndRecv() (*{{.Reply}}, error)
	{{- end}}
	{{- if and .ClientStreaming .ServerStreaming}}
	CloseSend() error
	{{- end}}
	{{$.HTTPIdent}}.ClientStream
}

{{- if .ServerStreaming}}
func (x *_{{$svrType}}_{{.Name}}HTTPServer) Send(m *{{.Reply}}) error {
	return x.ServerStream.SendMsg(m)
}
{{- end}}

{{- if .ClientStreaming}}
func (x *_{{$svrType}}_{{.Name}}HTTPServer) Recv() (*{{.Request}}, error) {
	m := new({{.Request}})
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}
{{- end}}

{{- if and .ClientStreaming (not .ServerStreaming)}}
func (x *_{{$svrType}}_{{.Name}}HTTPServer) SendAndClose(m *{{.Reply}}) error {
	return x.ServerStream.SendMsg(m)
}
{{- end}}
{{- end}}
{{end}}

{{range .Methods}}
func _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(srv {{$svrType}}HTTPServer) func(ctx {{$.HTTPIdent}}.Context) error {
	return func(ctx {{$.HTTPIdent}}.Context) error {
		{{- if .ClientStreaming}}
		stream, err := {{$.HTTPIdent}}.NewWebSocketServerStream(ctx{{if .BodyMessage}}, {{$.HTTPIdent}}.WithStreamBodyField("{{.BodyField}}"){{end}})
		if err != nil {
			return err
		}
		{{$.HTTPIdent}}.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		stream.SetContext(ctx)
		err = srv.{{.Name}}(&_{{$svrType}}_{{.Name}}HTTPServer{ServerStream: stream})
		return stream.Close(err)
		{{- else if .ServerStreaming}}
		{{- template "bindRequest" (bind $ .) -}}
		stream := {{$.HTTPIdent}}.NewServerSentEventServerStream(ctx)
		{{$.HTTPIdent}}.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		stream.SetContext(ctx)
		err := srv.{{.Name}}(&in, &_{{$svrType}}_{{.Name}}HTTPServer{ServerStream: stream})
		return stream.Close(err)
		{{- else}}
		{{- template "bindRequest" (bind $ .) -}}
		{{$.HTTPIdent}}.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		out, err := srv.{{.Name}}(ctx, &in)
		if err != nil {
			return err
		}
		reply := out
		{{- if or .ReplyHTTPBody .ResponseBodyHTTPBody}}
		return ctx.Blob(200, {{$.HTTPIdent}}.BodyContentType(reply{{.ResponseBodyGetter}}), reply{{.ResponseBodyGetter}}.GetData())
		{{- else if .ResponseBodyGetter}}
		return ctx.JSON(200, {{$.TranscodingIdent}}.NewProtoJSONField(reply, "{{.ResponseBodyField}}"))
		{{- else}}
		return ctx.JSON(200, {{$.TranscodingIdent}}.NewProtoJSON(reply))
		{{- end}}
		{{- end}}
	}
}
{{end}}

type {{.ServiceType}}HTTPClient interface {
{{- range .ClientMethods}}
	{{- if ne .Comment ""}}
	{{.Comment}}
	{{- end}}
	{{- if .ClientStreaming}}
	{{.Name}}(ctx {{$.ContextIdent}}.Context, opts ...{{$.HTTPIdent}}.CallOption) ({{$svrType}}_{{.Name}}HTTPClientStream, error)
	{{- else if .ServerStreaming}}
	{{.Name}}(ctx {{$.ContextIdent}}.Context, req *{{.Request}}, opts ...{{$.HTTPIdent}}.CallOption) ({{$svrType}}_{{.Name}}HTTPClientStream, error)
	{{- else}}
	{{.Name}}(ctx {{$.ContextIdent}}.Context, req *{{.Request}}, opts ...{{$.HTTPIdent}}.CallOption) (rsp *{{.Reply}}, err error)
	{{- end}}
{{- end}}
}

type {{.ServiceType}}HTTPClientImpl struct{
	cc *{{$.HTTPIdent}}.Client
}

func New{{.ServiceType}}HTTPClient (client *{{$.HTTPIdent}}.Client) {{.ServiceType}}HTTPClient {
	return &{{.ServiceType}}HTTPClientImpl{client}
}

{{range .ClientMethods}}
{{- if or .ClientStreaming .ServerStreaming}}
type {{$svrType}}_{{.Name}}HTTPClient struct {
	{{- if .ClientStreaming}}
	{{$.HTTPIdent}}.SendingClientStream
	ctx {{$.ContextIdent}}.Context
		cc *{{$.HTTPIdent}}.Client
		path *{{$.TranscodingIdent}}.CompiledPath
		opts []{{$.HTTPIdent}}.CallOption
	{{- else}}
	{{$.HTTPIdent}}.ClientStream
	{{- end}}
}

{{- if .ClientStreaming}}
func (x *{{$svrType}}_{{.Name}}HTTPClient) open(m *{{.Request}}) error {
	if x.SendingClientStream != nil {
		return nil
	}
	{{- if .BodyHTTPBody}}
	opts := append([]{{$.HTTPIdent}}.CallOption{
		{{$.HTTPIdent}}.WithContentType({{$.HTTPIdent}}.BodyContentType(m{{.BodyGetter}})),
	}, x.opts...)
	{{- else}}
	opts := x.opts
	{{- end}}
	path, err := x.path.Build(m)
	if err != nil {
		return err
	}
	stream, err := x.cc.WebSocket(x.ctx, path, opts...)
	if err != nil {
		return err
	}
	x.SendingClientStream = stream
	return nil
}

func (x *{{$svrType}}_{{.Name}}HTTPClient) CloseSend() error {
	if err := x.open(nil); err != nil {
		return err
	}
	return x.SendingClientStream.CloseSend()
}

func (x *{{$svrType}}_{{.Name}}HTTPClient) Send(m *{{.Request}}) error {
	if err := x.open(m); err != nil {
		return err
	}
		return x.SendingClientStream.SendMsg(m{{if .BodyMessage}}{{.BodyGetter}}{{end}})
}
{{- end}}

{{- if .ServerStreaming}}
func (x *{{$svrType}}_{{.Name}}HTTPClient) Recv() (*{{.Reply}}, error) {
	{{- if .ClientStreaming}}
	if err := x.open(nil); err != nil {
		return nil, err
	}
	{{- end}}
	m := new({{.Reply}})
	if err := x.{{if .ClientStreaming}}SendingClientStream{{else}}ClientStream{{end}}.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}
{{- end}}

{{- if and .ClientStreaming (not .ServerStreaming)}}
func (x *{{$svrType}}_{{.Name}}HTTPClient) CloseAndRecv() (*{{.Reply}}, error) {
	if err := x.open(nil); err != nil {
		return nil, err
	}
	m := new({{.Reply}})
	if err := x.SendingClientStream.CloseAndRecv(m); err != nil {
		return nil, err
	}
	return m, nil
}
{{- end}}
{{- end}}
{{end}}

{{range .ClientMethods}}
	{{- if ne .Comment ""}}
	{{.Comment}}
	{{- end}}
{{- if .ClientStreaming}}
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx {{$.ContextIdent}}.Context, opts ...{{$.HTTPIdent}}.CallOption) ({{$svrType}}_{{.Name}}HTTPClientStream, error) {
	{{- if .UnspecifiedMethod}}
	return nil, {{$.TranscodingIdent}}.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, {{$.TranscodingIdent}}.ErrUnboundPathWildcard
	{{- else}}
	pattern := "{{.PathTemplate}}"
	opts = append([]{{$.HTTPIdent}}.CallOption{
		{{$.HTTPIdent}}.WithAccept("application/protojson"),
		{{- if not .BodyHTTPBody}}
		{{$.HTTPIdent}}.WithContentType("application/protojson"),
		{{- end}}
		{{$.HTTPIdent}}.WithOperation(Operation{{$svrType}}{{.OriginalName}}),
		{{$.HTTPIdent}}.WithPathTemplate(pattern),
	}, opts...)
	return &{{$svrType}}_{{.Name}}HTTPClient{ctx: ctx, cc: c.cc, path: _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path, opts: opts}, nil
	{{- end}}
}
{{- else if .ServerStreaming}}
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx {{$.ContextIdent}}.Context, in *{{.Request}}, opts ...{{$.HTTPIdent}}.CallOption) ({{$svrType}}_{{.Name}}HTTPClientStream, error) {
	{{- if .UnspecifiedMethod}}
	return nil, {{$.TranscodingIdent}}.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, {{$.TranscodingIdent}}.ErrUnboundPathWildcard
	{{- else}}
	pattern := "{{.PathTemplate}}"
	path, err := _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path.Build(in)
	if err != nil {
		return nil, err
	}
	{{- if .HasBody}}
	opts = append([]{{$.HTTPIdent}}.CallOption{
		{{$.HTTPIdent}}.WithAccept("text/event-stream"),
			{{- if .BodyHTTPBody}}
			{{$.HTTPIdent}}.WithContentType({{$.HTTPIdent}}.BodyContentType(in{{.BodyGetter}})),
			{{- else if .BodyProtoJSON}}
			{{$.HTTPIdent}}.WithContentType("application/protojson"),
			{{- else}}
			{{$.HTTPIdent}}.WithContentType("application/json"),
		{{- end}}
		{{$.HTTPIdent}}.WithOperation(Operation{{$svrType}}{{.OriginalName}}),
		{{$.HTTPIdent}}.WithPathTemplate(pattern),
	}, opts...)
		stream, err := c.cc.ServerSentEvent(ctx, "{{.Method}}", path, in{{.BodyGetter}}, opts...)
	{{- else}}
	opts = append([]{{$.HTTPIdent}}.CallOption{
		{{$.HTTPIdent}}.WithAccept("text/event-stream"),
		{{$.HTTPIdent}}.WithContentType("application/protojson"),
		{{$.HTTPIdent}}.WithOperation(Operation{{$svrType}}{{.OriginalName}}),
		{{$.HTTPIdent}}.WithPathTemplate(pattern),
	}, opts...)
	stream, err := c.cc.ServerSentEvent(ctx, "{{.Method}}", path, nil, opts...)
	{{- end}}
	if err != nil {
		return nil, err
	}
	return &{{$svrType}}_{{.Name}}HTTPClient{ClientStream: stream}, nil
	{{- end}}
}
{{- else}}
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx {{$.ContextIdent}}.Context, in *{{.Request}}, opts ...{{$.HTTPIdent}}.CallOption) (*{{.Reply}}, error) {
	{{- if .UnspecifiedMethod}}
	return nil, {{$.TranscodingIdent}}.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, {{$.TranscodingIdent}}.ErrUnboundPathWildcard
	{{- else}}
	var out {{.Reply}}
	pattern := "{{.PathTemplate}}"
	path, err := _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path.Build(in)
	if err != nil {
		return nil, err
	}
	{{- if .HasBody}}
	opts = append([]{{$.HTTPIdent}}.CallOption{
			{{$.HTTPIdent}}.WithAccept("application/json"),
			{{- if .BodyHTTPBody}}
			{{$.HTTPIdent}}.WithContentType({{$.HTTPIdent}}.BodyContentType(in{{.BodyGetter}})),
			{{- else}}
			{{$.HTTPIdent}}.WithContentType("application/json"),
		{{- end}}
		{{$.HTTPIdent}}.WithOperation(Operation{{$svrType}}{{.OriginalName}}),
		{{$.HTTPIdent}}.WithPathTemplate(pattern),
	}, opts...)
	{{- else}}
	opts = append([]{{$.HTTPIdent}}.CallOption{
			{{$.HTTPIdent}}.WithAccept("application/json"),
		{{$.HTTPIdent}}.WithOperation(Operation{{$svrType}}{{.OriginalName}}),
		{{$.HTTPIdent}}.WithPathTemplate(pattern),
	}, opts...)
	{{- end}}
	{{- if .ResponseBodyHTTPBody}}
	var responseBody {{.ResponseBodyType}}
	{{- end}}
	{{- if .HasBody}}
		{{- if eq .BodyField "*"}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, {{$.TranscodingIdent}}.NewProtoJSON(in{{range .PathFields}}, "{{.}}"{{end}}), {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}{{$.TranscodingIdent}}.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}{{$.TranscodingIdent}}.NewProtoJSON(&out){{end}}, opts...)
		{{- else if .BodyHTTPBody}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, in{{.BodyGetter}}, {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}{{$.TranscodingIdent}}.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}{{$.TranscodingIdent}}.NewProtoJSON(&out){{end}}, opts...)
		{{- else}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, {{$.TranscodingIdent}}.NewProtoJSONField(in, "{{.BodyField}}"), {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}{{$.TranscodingIdent}}.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}{{$.TranscodingIdent}}.NewProtoJSON(&out){{end}}, opts...)
		{{- end}}
	{{- else}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, nil, {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}{{$.TranscodingIdent}}.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}{{$.TranscodingIdent}}.NewProtoJSON(&out){{end}}, opts...)
	{{- end}}
	if err != nil {
		return nil, err
	}
	{{- if .ResponseBodyHTTPBody}}
	{{.ResponseAssignment}}
	{{- end}}
	return &out, nil
	{{- end}}
}
{{- end}}
{{end}}

{{define "bindRequest"}}
		var in {{.Method.Request}}
		{{- if .Method.HasBody}}
			{{- if eq .Method.BodyField "*"}}
		if err := ctx.Bind({{.Service.TranscodingIdent}}.NewProtoJSON(&in{{range .Method.PathFields}}, "{{.}}"{{end}})); err != nil {
			return err
		}
			{{- else if .Method.BodyHTTPBody}}
		var body {{.Method.BodyType}}
		if err := ctx.Bind(&body); err != nil {
			return err
		}
		{{.Method.BodyAssignment}}
			{{- else}}
		if err := ctx.Bind({{.Service.TranscodingIdent}}.NewProtoJSONField(&in, "{{.Method.BodyField}}")); err != nil {
			return err
		}
			{{- end}}
		{{- end}}
		{{- if not .Method.HasBody}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- else if ne .Method.BodyField "*"}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- end}}
		{{- if .Method.HasVars}}
		if err := ctx.BindVars(&in); err != nil {
			return err
		}
		{{- end}}
{{end}}
