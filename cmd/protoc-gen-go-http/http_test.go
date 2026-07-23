package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPTemplateClientUsesCompiledPathAndGoogleJSON(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Greeter",
		ServiceName: "helloworld.Greeter",
		Methods: []*methodDesc{
			{
				Name:         "SayHello",
				OriginalName: "SayHello",
				Request:      "HelloRequest",
				Reply:        "HelloReply",
				Path:         "/helloworld/{name}",
				PathTemplate: "/helloworld/{name}",
				Method:       "GET",
				HasVars:      true,
			},
			{
				Name:          "CreateHello",
				OriginalName:  "CreateHello",
				Request:       "CreateHelloRequest",
				Reply:         "HelloReply",
				Path:          "/helloworld",
				PathTemplate:  "/helloworld",
				Method:        "POST",
				HasBody:       true,
				BodyField:     "*",
				BodyProtoJSON: true,
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		`var _Greeter_SayHello0_HTTP_Path = http.MustCompilePath("/helloworld/{name}", new(HelloRequest), http.WithQueryParams())`,
		`var _Greeter_CreateHello0_HTTP_Path = http.MustCompilePath("/helloworld", new(CreateHelloRequest))`,
		`path, err := _Greeter_SayHello0_HTTP_Path.Build(in)`,
		`path, err := _Greeter_CreateHello0_HTTP_Path.Build(in)`,
		`if err != nil`,
		`http.Accept("application/json")`,
		`http.ContentType("application/json")`,
		`http.NewProtoJSON(in)`,
		`http.NewProtoJSON(&out)`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "binding.") {
		t.Fatalf("generated template should not reference binding package:\n%s", got)
	}
}

func TestHTTPTemplateClientRejectsAmbiguousPrimaryRules(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Gateway",
		ServiceName: "example.Gateway",
		Methods: []*methodDesc{
			{
				Name:              "AnyMethod",
				OriginalName:      "AnyMethod",
				Request:           "Request",
				Reply:             "Reply",
				Path:              "/v1/any",
				PathTemplate:      "/v1/any",
				Method:            "*",
				UnspecifiedMethod: true,
			},
			{
				Name:                "BareWildcard",
				OriginalName:        "BareWildcard",
				Request:             "Request",
				Reply:               "Reply",
				Path:                "/v1/*",
				PathTemplate:        "/v1/*",
				Method:              "GET",
				UnboundPathWildcard: true,
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		"return nil, http.ErrUnspecifiedHTTPMethod",
		"return nil, http.ErrUnboundPathWildcard",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
}

func TestHTTPTemplateStreamsAndHTTPBody(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Greeter",
		ServiceName: "helloworld.Greeter",
		Methods: []*methodDesc{
			{
				Name:            "ListHello",
				OriginalName:    "ListHello",
				Request:         "ListHelloRequest",
				Reply:           "HelloReply",
				Path:            "/helloworld",
				PathTemplate:    "/helloworld",
				Method:          "GET",
				ServerStreaming: true,
			},
			{
				Name:            "ChatHello",
				OriginalName:    "ChatHello",
				Request:         "HelloRequest",
				Reply:           "HelloReply",
				Path:            "/helloworld/chat",
				PathTemplate:    "/helloworld/chat",
				Method:          "POST",
				ClientStreaming: true,
				ServerStreaming: true,
			},
			{
				Name:                 "UploadHello",
				OriginalName:         "UploadHello",
				Request:              "UploadHelloRequest",
				Reply:                "UploadHelloReply",
				Path:                 "/helloworld/upload",
				PathTemplate:         "/helloworld/upload",
				Method:               "POST",
				HasBody:              true,
				BodyField:            "body",
				BodyQueryName:        "body",
				BodyGetter:           ".GetBody()",
				BodyType:             "*HTTPBody",
				BodyAssignment:       "in.SetBody(body)",
				BodyHTTPBody:         true,
				BodyMessage:          true,
				BodyProtoJSON:        true,
				ResponseBodyGetter:   ".GetBody()",
				ResponseBodyField:    "body",
				ResponseBodyType:     "*HTTPBody",
				ResponseAssignment:   "out.SetBody(responseBody)",
				ResponseBodyHTTPBody: true,
			},
			{
				Name:            "ChatData",
				OriginalName:    "ChatData",
				Request:         "ChatDataRequest",
				Reply:           "ChatDataReply",
				Path:            "/v1/bitto/chat",
				PathTemplate:    "/v1/bitto/chat",
				Method:          "GET",
				HasBody:         true,
				BodyField:       "data",
				BodyQueryName:   "data",
				BodyGetter:      ".GetData()",
				BodyType:        "*Data",
				BodyAssignment:  "in.SetData(body)",
				BodyMessage:     true,
				BodyProtoJSON:   true,
				ClientStreaming: true,
				ServerStreaming: true,
			},
			{
				// Client-streaming RPC whose named body is a scalar field: it is not
				// streamable frame-by-frame, so the whole message is sent/decoded.
				Name:            "ChatText",
				OriginalName:    "ChatText",
				Request:         "ChatTextRequest",
				Reply:           "ChatTextReply",
				Path:            "/v1/bitto/text",
				PathTemplate:    "/v1/bitto/text",
				Method:          "GET",
				HasBody:         true,
				BodyField:       "text",
				BodyQueryName:   "text",
				BodyGetter:      ".GetText()",
				BodyType:        "string",
				BodyAssignment:  "in.SetText(body)",
				BodyMessage:     false,
				ClientStreaming: true,
				ServerStreaming: true,
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		`ListHello(*ListHelloRequest, Greeter_ListHelloServer) error`,
		`stream := http.NewServerSentEventServerStream(ctx)`,
		`stream, err := c.cc.ServerSentEvent(ctx, "GET", path, nil, opts...)`,
		`ChatHello(Greeter_ChatHelloServer) error`,
		`stream, err := http.NewWebSocketServerStream(ctx)`,
		`func (x *Greeter_ChatHelloHTTPClient) open(m *HelloRequest) error`,
		`path *http.CompiledPath`,
		`path, err := x.path.Build(m)`,
		`stream, err := x.cc.WebSocket(x.ctx, path, opts...)`,
		`http.ContentType("application/protojson")`,
		`return &Greeter_ChatHelloHTTPClient{ctx: ctx, cc: c.cc, path: _Greeter_ChatHello0_HTTP_Path, opts: opts}, nil`,
		`http.ContentType(http.BodyContentType(in.GetBody()))`,
		`http.WithOmitFields("body")`,
		`return ctx.Blob(200, http.BodyContentType(reply.GetBody()), reply.GetBody().GetData())`,
		// Client-streaming RPC with a streamable (message-kind) named body field.
		`stream, err := http.NewWebSocketServerStream(ctx, http.WithStreamBodyField("data"))`,
		`return x.ClientStream.Send(m.GetData())`,
		// Client-streaming RPC with a scalar named body: whole message is streamed.
		`func (x *Greeter_ChatTextHTTPClient) Send(m *ChatTextRequest) error`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
	// The whole-message streaming client (ChatHello) and the scalar-body streaming
	// client (ChatText) must both send the whole message, not a sub-field.
	if !strings.Contains(got, "return x.ClientStream.Send(m)\n") {
		t.Fatalf("generated template should send whole message for non-streamable body:\n%s", got)
	}
	// The scalar-body server handler must NOT receive a stream body-field option.
	if strings.Contains(got, `http.WithStreamBodyField("text")`) {
		t.Fatalf("scalar named body should not emit WithStreamBodyField:\n%s", got)
	}
}

func TestOpaqueGeneratedCodeCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping protoc integration test in short mode")
	}
	for _, tool := range []string{"go", "protoc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed: %v", tool, err)
		}
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	protobufDir := strings.TrimSpace(runCommand(t, ".", "go", "list", "-m", "-f", "{{.Dir}}", "google.golang.org/protobuf"))
	protocPath, err := exec.LookPath("protoc")
	if err != nil {
		t.Fatal(err)
	}
	protocPath, err = filepath.EvalSymlinks(protocPath)
	if err != nil {
		t.Fatal(err)
	}
	protocInclude := filepath.Join(filepath.Dir(filepath.Dir(protocPath)), "include")
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(bin, "protoc-gen-go-http")
	runCommand(t, ".", "go", "build", "-o", plugin, ".")
	runCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")

	protocArgs := []string{
		"-I", "testdata",
		"-I", protocInclude,
		"-I", filepath.Join(protobufDir, "src"),
		"-I", filepath.Join(root, "third_party"),
		"--go_out=" + tmp,
		"--go_opt=module=opaque.test",
		"--go-http_out=" + tmp,
		"--go-http_opt=module=opaque.test",
		"opaque/opaque.proto",
		"open/open.proto",
	}
	generate := func() ([]byte, error) {
		cmd := exec.Command("protoc", protocArgs...)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		return cmd.CombinedOutput()
	}
	if output, err := generate(); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}
	generatedPath := filepath.Join(tmp, "openpb", "open_http.pb.go")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`r.Handle("GET", "/v1/{name=items/*}", _OpenService_Alternate0_HTTP_Handler(srv))`,
		`r.Handle("GET", "/v1/alternate/{name=items/*}", _OpenService_Alternate1_HTTP_Handler(srv))`,
		`r.Handle("REPORT", "/v1/report/{name=items/*}", _OpenService_Alternate2_HTTP_Handler(srv))`,
		`r.Handle("*", "/v1/any/{name}", _OpenService_AnyMethod0_HTTP_Handler(srv))`,
		`pattern := "/v1/{name=items/*}"`,
		`return nil, http.ErrUnspecifiedHTTPMethod`,
		`return nil, http.ErrUnboundPathWildcard`,
	} {
		if !bytes.Contains(generated, []byte(want)) {
			t.Fatalf("generated output missing %q:\n%s", want, generated)
		}
	}
	if output, err := generate(); err != nil {
		t.Fatalf("second protoc run failed: %v\n%s", err, output)
	}
	regenerated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, regenerated) {
		t.Fatal("generated output changed between identical protoc runs")
	}

	goMod := fmt.Sprintf("module opaque.test\n\ngo 1.27rc2\n\nrequire github.com/openkratos/kratos v0.0.0\n\nreplace github.com/openkratos/kratos => %s\n", root)
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	conformanceTest := `package conformance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/protobuf/proto"

	kratoshttp "github.com/openkratos/kratos/transport/http"
	openpb "opaque.test/openpb"
	testpb "opaque.test/testpb"
)

type opaqueService struct{}

func (*opaqueService) MessageBody(_ context.Context, in *testpb.MessageRequest) (*testpb.MessageReply, error) {
	out := new(testpb.MessageReply)
	echoField(in, out, "payload")
	return out, nil
}

func (*opaqueService) ScalarBody(_ context.Context, in *testpb.ScalarRequest) (*testpb.ScalarReply, error) {
	out := new(testpb.ScalarReply)
	echoField(in, out, "text")
	return out, nil
}

func (*opaqueService) RepeatedBody(_ context.Context, in *testpb.RepeatedRequest) (*testpb.RepeatedReply, error) {
	out := new(testpb.RepeatedReply)
	echoField(in, out, "tags")
	return out, nil
}

func (*opaqueService) MapBody(_ context.Context, in *testpb.MapRequest) (*testpb.MapReply, error) {
	out := new(testpb.MapReply)
	echoField(in, out, "labels")
	return out, nil
}

func (*opaqueService) WholeBody(_ context.Context, in *testpb.WholeRequest) (*testpb.WholeReply, error) {
	out := new(testpb.WholeReply)
	echoMessage(in, out)
	return out, nil
}

type openService struct {
	mu       sync.Mutex
	lastName string
}

func (*openService) ScalarBody(_ context.Context, in *openpb.ScalarRequest) (*openpb.ScalarReply, error) {
	out := new(openpb.ScalarReply)
	echoField(in, out, "text")
	return out, nil
}

func (*openService) OneofBody(_ context.Context, in *openpb.OneofRequest) (*openpb.OneofReply, error) {
	out := new(openpb.OneofReply)
	echoField(in, out, "choice")
	return out, nil
}

func (s *openService) Alternate(_ context.Context, in *openpb.RouteRequest) (*openpb.RouteReply, error) {
	s.mu.Lock()
	s.lastName = fieldJSON(in, "name")
	s.mu.Unlock()
	out := new(openpb.RouteReply)
	echoMessage(in, out)
	return out, nil
}

func (s *openService) AnyMethod(ctx context.Context, in *openpb.RouteRequest) (*openpb.RouteReply, error) {
	return s.Alternate(ctx, in)
}

func (s *openService) BareWildcard(ctx context.Context, in *openpb.RouteRequest) (*openpb.RouteReply, error) {
	return s.Alternate(ctx, in)
}

func (s *openService) name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastName
}

type recordingTransport struct {
	mu   sync.Mutex
	url  string
	body string
}

func (r *recordingTransport) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	r.mu.Lock()
	r.url = req.URL.RequestURI()
	r.body = string(body)
	r.mu.Unlock()
	return stdhttp.DefaultTransport.RoundTrip(req)
}

func (r *recordingTransport) snapshot() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.url, r.body
}

func TestGeneratedGoogleHTTPConformance(t *testing.T) {
	server := kratoshttp.NewServer()
	openImpl := new(openService)
	openpb.RegisterOpenServiceHTTPServer(server, openImpl)
	testpb.RegisterOpaqueServiceHTTPServer(server, new(opaqueService))
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	recorder := new(recordingTransport)
	client, err := kratoshttp.NewClient(t.Context(), kratoshttp.WithEndpoint(httpServer.URL), kratoshttp.WithTransport(recorder))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	opaqueClient := testpb.NewOpaqueServiceHTTPClient(client)
	projectionCases := []struct {
		name  string
		field string
		value string
		call  func(proto.Message) (proto.Message, error)
		in    proto.Message
	}{
		{name: "message", field: "payload", value: ` + "`" + `{"value":"hello"}` + "`" + `, in: new(testpb.MessageRequest), call: func(in proto.Message) (proto.Message, error) { return opaqueClient.MessageBody(t.Context(), in.(*testpb.MessageRequest)) }},
		{name: "scalar", field: "text", value: ` + "`" + `"hello"` + "`" + `, in: new(testpb.ScalarRequest), call: func(in proto.Message) (proto.Message, error) { return opaqueClient.ScalarBody(t.Context(), in.(*testpb.ScalarRequest)) }},
		{name: "repeated", field: "tags", value: ` + "`" + `["a","b"]` + "`" + `, in: new(testpb.RepeatedRequest), call: func(in proto.Message) (proto.Message, error) { return opaqueClient.RepeatedBody(t.Context(), in.(*testpb.RepeatedRequest)) }},
		{name: "map", field: "labels", value: ` + "`" + `{"env":"prod"}` + "`" + `, in: new(testpb.MapRequest), call: func(in proto.Message) (proto.Message, error) { return opaqueClient.MapBody(t.Context(), in.(*testpb.MapRequest)) }},
	}
	for _, tt := range projectionCases {
		t.Run("opaque_"+tt.name, func(t *testing.T) {
			setFieldJSON(t, tt.in, tt.field, tt.value)
			out, err := tt.call(tt.in)
			if err != nil {
				t.Fatal(err)
			}
			if got := fieldJSON(out, tt.field); got != tt.value {
				t.Fatalf("projected field = %s, want %s", got, tt.value)
			}
		})
	}
	scalarWithQuery := new(testpb.ScalarRequest)
	setFieldJSON(t, scalarWithQuery, "text", ` + "`" + `"body-only"` + "`" + `)
	setFieldJSON(t, scalarWithQuery, "query", ` + "`" + `"query-only"` + "`" + `)
	if _, err := opaqueClient.ScalarBody(t.Context(), scalarWithQuery); err != nil {
		t.Fatal(err)
	}
	requestURL, requestBody := recorder.snapshot()
	if requestURL != "/v1/scalar?query=query-only" || requestBody != ` + "`" + `"body-only"` + "`" + ` {
		t.Fatalf("named body classification: url=%s body=%s", requestURL, requestBody)
	}
	whole := new(testpb.WholeRequest)
	setFieldJSON(t, whole, "name", ` + "`" + `"items/whole"` + "`" + `)
	setFieldJSON(t, whole, "count", ` + "`" + `"9007199254740993"` + "`" + `)
	wholeReply, err := opaqueClient.WholeBody(t.Context(), whole)
	if err != nil {
		t.Fatal(err)
	}
	requestURL, requestBody = recorder.snapshot()
	if requestURL != "/v1/whole/items%2Fwhole" || strings.Contains(requestBody, "name") || !strings.Contains(requestBody, ` + "`" + `"count":"9007199254740993"` + "`" + `) {
		t.Fatalf("whole body classification: url=%s body=%s", requestURL, requestBody)
	}
	if got := fieldJSON(wholeReply, "name"); got != ` + "`" + `"items/whole"` + "`" + ` {
		t.Fatalf("whole body path precedence = %s", got)
	}

	openClient := openpb.NewOpenServiceHTTPClient(client)
	openScalar := new(openpb.ScalarRequest)
	setFieldJSON(t, openScalar, "text", ` + "`" + `"open"` + "`" + `)
	openScalarReply, err := openClient.ScalarBody(t.Context(), openScalar)
	if err != nil || fieldJSON(openScalarReply, "text") != ` + "`" + `"open"` + "`" + ` {
		t.Fatalf("open scalar round trip: reply=%v err=%v", openScalarReply, err)
	}
	openOneof := new(openpb.OneofRequest)
	setFieldJSON(t, openOneof, "choice", ` + "`" + `"selected"` + "`" + `)
	openOneofReply, err := openClient.OneofBody(t.Context(), openOneof)
	if err != nil || fieldJSON(openOneofReply, "choice") != ` + "`" + `"selected"` + "`" + ` {
		t.Fatalf("open oneof round trip: reply=%v err=%v", openOneofReply, err)
	}

	primary := new(openpb.RouteRequest)
	setFieldJSON(t, primary, "name", ` + "`" + `"items/a%2Fb"` + "`" + `)
	setFieldJSON(t, primary, "query", ` + "`" + `"search"` + "`" + `)
	setFieldJSON(t, primary, "tags", ` + "`" + `["a","b"]` + "`" + `)
	setFieldJSON(t, primary, "filter", ` + "`" + `{"text":"nested"}` + "`" + `)
	primaryReply, err := openClient.Alternate(t.Context(), primary)
	if err != nil {
		t.Fatal(err)
	}
	if got := fieldJSON(primaryReply, "name"); got != ` + "`" + `"items/a%2Fb"` + "`" + ` {
		t.Fatalf("primary path round trip = %s", got)
	}
	requestURL, _ = recorder.snapshot()
	if requestURL != "/v1/items/a%252Fb?filter.text=nested&query=search&tags=a&tags=b" {
		t.Fatalf("query classification URL = %s", requestURL)
	}

	rawCases := []struct {
		method string
		path   string
		want   string
	}{
		{method: stdhttp.MethodGet, path: "/v1/alternate/items/x", want: ` + "`" + `"items/x"` + "`" + `},
		{method: stdhttp.MethodGet, path: "/v1/alternate/items/a%2Fb", want: ` + "`" + `"items/a%2Fb"` + "`" + `},
		{method: "REPORT", path: "/v1/report/items/y", want: ` + "`" + `"items/y"` + "`" + `},
		{method: "BREW", path: "/v1/any/z", want: ` + "`" + `"z"` + "`" + `},
	}
	for _, tt := range rawCases {
		req, err := stdhttp.NewRequestWithContext(t.Context(), tt.method, httpServer.URL+tt.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := stdhttp.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(res.Body)
		res.Body.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if res.StatusCode != stdhttp.StatusOK || !strings.Contains(string(body), tt.want) {
			t.Fatalf("%s %s: status=%d body=%s", tt.method, tt.path, res.StatusCode, body)
		}
		if got := openImpl.name(); got != tt.want {
			t.Fatalf("%s %s extracted name = %s, want %s", tt.method, tt.path, got, tt.want)
		}
	}

	if _, err := openClient.AnyMethod(t.Context(), new(openpb.RouteRequest)); !errors.Is(err, kratoshttp.ErrUnspecifiedHTTPMethod) {
		t.Fatalf("AnyMethod error = %v", err)
	}
	if _, err := openClient.BareWildcard(t.Context(), new(openpb.RouteRequest)); !errors.Is(err, kratoshttp.ErrUnboundPathWildcard) {
		t.Fatalf("BareWildcard error = %v", err)
	}
}

func echoField(in, out proto.Message, field string) {
	data, err := json.Marshal(kratoshttp.NewProtoJSONField(in, field))
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, kratoshttp.NewProtoJSONField(out, field)); err != nil {
		panic(err)
	}
}

func echoMessage(in, out proto.Message) {
	data, err := json.Marshal(kratoshttp.NewProtoJSON(in))
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, kratoshttp.NewProtoJSON(out)); err != nil {
		panic(err)
	}
}

func setFieldJSON(t *testing.T, message proto.Message, field, value string) {
	t.Helper()
	if err := json.Unmarshal([]byte(value), kratoshttp.NewProtoJSONField(message, field)); err != nil {
		t.Fatal(err)
	}
}

func fieldJSON(message proto.Message, field string) string {
	data, err := json.Marshal(kratoshttp.NewProtoJSONField(message, field))
	if err != nil {
		panic(err)
	}
	return string(data)
}
`
	if err := os.WriteFile(filepath.Join(tmp, "conformance_test.go"), []byte(conformanceTest), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, tmp, "go", "test", "-mod=mod", "./...")
}

func TestInvalidHTTPRulesFailGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping protoc integration test in short mode")
	}
	for _, tool := range []string{"go", "protoc"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed: %v", tool, err)
		}
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	protocPath, err := exec.LookPath("protoc")
	if err != nil {
		t.Fatal(err)
	}
	protocPath, err = filepath.EvalSymlinks(protocPath)
	if err != nil {
		t.Fatal(err)
	}
	protocInclude := filepath.Join(filepath.Dir(filepath.Dir(protocPath)), "include")
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(bin, "protoc-gen-go-http")
	runCommand(t, ".", "go", "build", "-o", plugin, ".")

	const header = `syntax = "proto3";
package invalid;
import "google/api/annotations.proto";
option go_package = "invalid.test/api;api";
message Reply {}
`
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "missing path field",
			source: `message Request { string name = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/{missing}" }; } }`,
			want: `path field "missing" does not exist`,
		},
		{
			name: "repeated path field",
			source: `message Request { repeated string names = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/{names}" }; } }`,
			want: `path field "names" is repeated or mapped`,
		},
		{
			name: "message path leaf",
			source: `message Child { string name = 1; } message Request { Child child = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/{child}" }; } }`,
			want: `path field "child" is a message`,
		},
		{
			name: "nested body",
			source: `message Child { string name = 1; } message Request { Child child = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { post: "/v1/get" body: "child.name" }; } }`,
			want: `body field "child.name" must be top-level`,
		},
		{
			name: "response star",
			source: `message Request {}
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/get" response_body: "*" }; } }`,
			want: `response_body "*" is invalid`,
		},
		{
			name: "nested additional binding",
			source: `message Request {}
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/get" additional_bindings { get: "/v1/alt" additional_bindings { get: "/v1/nested" } } }; } }`,
			want: `nested additional bindings are not allowed`,
		},
		{
			name: "duplicate match set",
			source: `message Request { string first = 1; string second = 2; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/{first}" additional_bindings { get: "/v1/{second}" } }; } }`,
			want: `duplicate HTTP match set`,
		},
		{
			name: "conflicting patterns",
			source: `message Request { string first = 1; string second = 2; }
service API {
  rpc First(Request) returns (Reply) { option (google.api.http) = { get: "/v1/{first}/tail" }; }
  rpc Second(Request) returns (Reply) { option (google.api.http) = { get: "/v1/head/{second}" }; }
}`,
			want: `conflicting HTTP rule`,
		},
		{
			name: "map query field",
			source: `message Request { map<string, string> labels = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/get" }; } }`,
			want: `field "labels" is a map and cannot be encoded as a query parameter`,
		},
		{
			name: "repeated message query field",
			source: `message Child { string name = 1; } message Request { repeated Child children = 1; }
service API { rpc Get(Request) returns (Reply) { option (google.api.http) = { get: "/v1/get" }; } }`,
			want: `field "children" is a repeated message and cannot be encoded as a query parameter`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(tmp, strings.ReplaceAll(tt.name, " ", "-"))
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			protoFile := filepath.Join(dir, "invalid.proto")
			if err := os.WriteFile(protoFile, []byte(header+tt.source), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(
				"protoc",
				"-I", dir,
				"-I", protocInclude,
				"-I", filepath.Join(root, "third_party"),
				"--go-http_out="+dir,
				"invalid.proto",
			)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("protoc unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), "RPC invalid.API.") || !strings.Contains(string(output), tt.want) {
				t.Fatalf("protoc output missing RPC context or %q:\n%s", tt.want, output)
			}
			generated, err := filepath.Glob(filepath.Join(dir, "*_http.pb.go"))
			if err != nil {
				t.Fatal(err)
			}
			if len(generated) != 0 {
				t.Fatalf("partial generated files: %v", generated)
			}
		})
	}
}

func runCommand(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, output)
	}
	return string(output)
}
