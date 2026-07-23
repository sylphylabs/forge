{{$svrType := .ServiceType}}
{{$svrName := .ServiceName}}

{{- range .ClientMethods}}
const Operation{{$svrType}}{{.OriginalName}} = "/{{$svrName}}/{{.OriginalName}}"
{{- end}}

{{- range .ClientMethods}}
{{- if and (not .UnspecifiedMethod) (not .UnboundPathWildcard)}}
var _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path = http.MustCompilePath("{{.PathTemplate}}", new({{.Request}}){{if .HasBody}}{{if and (ne .BodyField "*") (ne .BodyField "")}}, http.WithQueryParams(), http.WithOmitFields("{{.BodyQueryName}}"){{end}}{{else}}, http.WithQueryParams(){{end}})
{{- end}}
{{- end}}

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
	{{.Name}}(context.Context, *{{.Request}}) (*{{.Reply}}, error)
	{{- end}}
{{- end}}
}

func Register{{.ServiceType}}HTTPServer(s *http.Server, srv {{.ServiceType}}HTTPServer) {
	r := s.Route("/")
	{{- range .Methods}}
	{{- if .ClientStreaming}}
	r.Handle("GET", "{{.Path}}", _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(s, srv))
	{{- else}}
	r.Handle("{{.Method}}", "{{.Path}}", _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(s, srv))
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
	http.ServerStream
}

type _{{$svrType}}_{{.Name}}HTTPServer struct {
	http.ServerStream
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
func _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Handler(s *http.Server, srv {{$svrType}}HTTPServer) func(ctx http.Context) error {
	{{- if and (not .ClientStreaming) (not .ServerStreaming)}}
	h := s.WrapMiddleware(Operation{{$svrType}}{{.OriginalName}}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.{{.Name}}(ctx, req.(*{{.Request}}))
	})
	{{- end}}
	return func(ctx http.Context) error {
		{{- if .ClientStreaming}}
		stream, err := http.NewWebSocketServerStream(ctx{{if .BodyMessage}}, http.WithStreamBodyField("{{.BodyField}}"){{end}})
		if err != nil {
			return err
		}
		http.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		h := ctx.Middleware(func(ctx context.Context, req interface{}) (interface{}, error) {
			stream.SetContext(ctx)
			return nil, srv.{{.Name}}(&_{{$svrType}}_{{.Name}}HTTPServer{ServerStream: stream})
		})
		_, err = h(ctx, nil)
		return stream.Close(err)
		{{- else if .ServerStreaming}}
		var in {{.Request}}
		{{- if .HasBody}}
			{{- if eq .BodyField "*"}}
		if err := ctx.Bind(http.NewProtoJSON(&in{{range .PathFields}}, "{{.}}"{{end}})); err != nil {
			return err
		}
			{{- else if .BodyHTTPBody}}
		var body {{.BodyType}}
		if err := ctx.Bind(&body); err != nil {
			return err
		}
		{{.BodyAssignment}}
			{{- else}}
		if err := ctx.Bind(http.NewProtoJSONField(&in, "{{.BodyField}}")); err != nil {
			return err
		}
			{{- end}}
		{{- end}}
		{{- if not .HasBody}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- else if ne .BodyField "*"}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- end}}
		{{- if .HasVars}}
		if err := ctx.BindVars(&in); err != nil {
			return err
		}
		{{- end}}
		stream := http.NewServerSentEventServerStream(ctx)
		http.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		h := ctx.Middleware(func(ctx context.Context, req interface{}) (interface{}, error) {
			stream.SetContext(ctx)
			return nil, srv.{{.Name}}(req.(*{{.Request}}), &_{{$svrType}}_{{.Name}}HTTPServer{ServerStream: stream})
		})
		_, err := h(ctx, &in)
		return stream.Close(err)
		{{- else}}
		var in {{.Request}}
		{{- if .HasBody}}
			{{- if eq .BodyField "*"}}
		if err := ctx.Bind(http.NewProtoJSON(&in{{range .PathFields}}, "{{.}}"{{end}})); err != nil {
			return err
		}
			{{- else if .BodyHTTPBody}}
		var body {{.BodyType}}
		if err := ctx.Bind(&body); err != nil {
			return err
		}
		{{.BodyAssignment}}
			{{- else}}
		if err := ctx.Bind(http.NewProtoJSONField(&in, "{{.BodyField}}")); err != nil {
			return err
		}
			{{- end}}
		{{- end}}
		{{- if not .HasBody}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- else if ne .BodyField "*"}}
		if err := ctx.BindQuery(&in); err != nil {
			return err
		}
		{{- end}}
		{{- if .HasVars}}
		if err := ctx.BindVars(&in); err != nil {
			return err
		}
		{{- end}}
		http.SetOperation(ctx,Operation{{$svrType}}{{.OriginalName}})
		out, err := h(ctx, &in)
		if err != nil {
			return err
		}
		reply := out.(*{{.Reply}})
		{{- if or .ReplyHTTPBody .ResponseBodyHTTPBody}}
		return ctx.Blob(200, http.BodyContentType(reply{{.ResponseBodyGetter}}), reply{{.ResponseBodyGetter}}.GetData())
		{{- else if .ResponseBodyGetter}}
		return ctx.JSON(200, http.NewProtoJSONField(reply, "{{.ResponseBodyField}}"))
		{{- else}}
		return ctx.JSON(200, http.NewProtoJSON(reply))
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
	{{.Name}}(ctx context.Context, opts ...http.CallOption) ({{$svrType}}_{{.Name}}Client, error)
	{{- else if .ServerStreaming}}
	{{.Name}}(ctx context.Context, req *{{.Request}}, opts ...http.CallOption) ({{$svrType}}_{{.Name}}Client, error)
	{{- else}}
	{{.Name}}(ctx context.Context, req *{{.Request}}, opts ...http.CallOption) (rsp *{{.Reply}}, err error)
	{{- end}}
{{- end}}
}

type {{.ServiceType}}HTTPClientImpl struct{
	cc *http.Client
}

func New{{.ServiceType}}HTTPClient (client *http.Client) {{.ServiceType}}HTTPClient {
	return &{{.ServiceType}}HTTPClientImpl{client}
}

{{range .ClientMethods}}
{{- if or .ClientStreaming .ServerStreaming}}
type {{$svrType}}_{{.Name}}HTTPClient struct {
	http.ClientStream
	{{- if .ClientStreaming}}
	ctx context.Context
		cc *http.Client
		path *http.CompiledPath
		opts []http.CallOption
	{{- end}}
}

{{- if .ClientStreaming}}
func (x *{{$svrType}}_{{.Name}}HTTPClient) open(m *{{.Request}}) error {
	if x.ClientStream != nil {
		return nil
	}
	{{- if .BodyHTTPBody}}
	opts := append([]http.CallOption{
		http.ContentType(http.BodyContentType(m{{.BodyGetter}})),
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
	x.ClientStream = stream
	return nil
}

func (x *{{$svrType}}_{{.Name}}HTTPClient) CloseSend() error {
	if err := x.open(nil); err != nil {
		return err
	}
	return x.ClientStream.CloseSend()
}

func (x *{{$svrType}}_{{.Name}}HTTPClient) Send(m *{{.Request}}) error {
	if err := x.open(m); err != nil {
		return err
	}
		return x.ClientStream.Send(m{{if .BodyMessage}}{{.BodyGetter}}{{end}})
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
	if err := x.ClientStream.Recv(m); err != nil {
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
	if err := x.ClientStream.CloseAndRecv(m); err != nil {
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
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx context.Context, opts ...http.CallOption) ({{$svrType}}_{{.Name}}Client, error) {
	{{- if .UnspecifiedMethod}}
	return nil, http.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, http.ErrUnboundPathWildcard
	{{- else}}
	pattern := "{{.PathTemplate}}"
	opts = append([]http.CallOption{
		http.Accept("application/protojson"),
		{{- if not .BodyHTTPBody}}
		http.ContentType("application/protojson"),
		{{- end}}
		http.Operation(Operation{{$svrType}}{{.OriginalName}}),
		http.PathTemplate(pattern),
	}, opts...)
	return &{{$svrType}}_{{.Name}}HTTPClient{ctx: ctx, cc: c.cc, path: _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path, opts: opts}, nil
	{{- end}}
}
{{- else if .ServerStreaming}}
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx context.Context, in *{{.Request}}, opts ...http.CallOption) ({{$svrType}}_{{.Name}}Client, error) {
	{{- if .UnspecifiedMethod}}
	return nil, http.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, http.ErrUnboundPathWildcard
	{{- else}}
	pattern := "{{.PathTemplate}}"
	path, err := _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path.Build(in)
	if err != nil {
		return nil, err
	}
	{{- if .HasBody}}
	opts = append([]http.CallOption{
		http.Accept("text/event-stream"),
			{{- if .BodyHTTPBody}}
			http.ContentType(http.BodyContentType(in{{.BodyGetter}})),
			{{- else if .BodyProtoJSON}}
			http.ContentType("application/protojson"),
			{{- else}}
			http.ContentType("application/json"),
		{{- end}}
		http.Operation(Operation{{$svrType}}{{.OriginalName}}),
		http.PathTemplate(pattern),
	}, opts...)
		stream, err := c.cc.ServerSentEvent(ctx, "{{.Method}}", path, in{{.BodyGetter}}, opts...)
	{{- else}}
	opts = append([]http.CallOption{
		http.Accept("text/event-stream"),
		http.ContentType("application/protojson"),
		http.Operation(Operation{{$svrType}}{{.OriginalName}}),
		http.PathTemplate(pattern),
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
func (c *{{$svrType}}HTTPClientImpl) {{.Name}}(ctx context.Context, in *{{.Request}}, opts ...http.CallOption) (*{{.Reply}}, error) {
	{{- if .UnspecifiedMethod}}
	return nil, http.ErrUnspecifiedHTTPMethod
	{{- else if .UnboundPathWildcard}}
	return nil, http.ErrUnboundPathWildcard
	{{- else}}
	var out {{.Reply}}
	pattern := "{{.PathTemplate}}"
	path, err := _{{$svrType}}_{{.Name}}{{.Num}}_HTTP_Path.Build(in)
	if err != nil {
		return nil, err
	}
	{{- if .HasBody}}
	opts = append([]http.CallOption{
			http.Accept("application/json"),
			{{- if .BodyHTTPBody}}
			http.ContentType(http.BodyContentType(in{{.BodyGetter}})),
			{{- else}}
			http.ContentType("application/json"),
		{{- end}}
		http.Operation(Operation{{$svrType}}{{.OriginalName}}),
		http.PathTemplate(pattern),
	}, opts...)
	{{- else}}
	opts = append([]http.CallOption{
			http.Accept("application/json"),
		http.Operation(Operation{{$svrType}}{{.OriginalName}}),
		http.PathTemplate(pattern),
	}, opts...)
	{{- end}}
	{{- if .ResponseBodyHTTPBody}}
	var responseBody {{.ResponseBodyType}}
	{{- end}}
	{{- if .HasBody}}
		{{- if eq .BodyField "*"}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, http.NewProtoJSON(in{{range .PathFields}}, "{{.}}"{{end}}), {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}http.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}http.NewProtoJSON(&out){{end}}, opts...)
		{{- else if .BodyHTTPBody}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, in{{.BodyGetter}}, {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}http.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}http.NewProtoJSON(&out){{end}}, opts...)
		{{- else}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, http.NewProtoJSONField(in, "{{.BodyField}}"), {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}http.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}http.NewProtoJSON(&out){{end}}, opts...)
		{{- end}}
	{{- else}}
	err = c.cc.Invoke(ctx, "{{.Method}}", path, nil, {{if .ResponseBodyHTTPBody}}&responseBody{{else if .ResponseBodyGetter}}http.NewProtoJSONField(&out, "{{.ResponseBodyField}}"){{else if .ReplyHTTPBody}}&out{{else}}http.NewProtoJSON(&out){{end}}, opts...)
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
