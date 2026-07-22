package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNoParameters(t *testing.T) {
	path := "/test/noparams"
	m := buildPathVars(path)
	if !reflect.DeepEqual(m, map[string]*string{}) {
		t.Fatalf("Map should be empty")
	}
}

func TestSingleParam(t *testing.T) {
	path := "/test/{message.id}"
	m := buildPathVars(path)
	if !reflect.DeepEqual(len(m), 1) {
		t.Fatalf("len(m) not is 1")
	}
	if m["message.id"] != nil {
		t.Fatalf(`m["message.id"] should be empty`)
	}
}

func TestTwoParametersReplacement(t *testing.T) {
	path := "/test/{message.id}/{message.name=messages/*}"
	m := buildPathVars(path)
	if len(m) != 2 {
		t.Fatal("len(m) should be 2")
	}
	if m["message.id"] != nil {
		t.Fatal(`m["message.id"] should be nil`)
	}
	if m["message.name"] == nil {
		t.Fatal(`m["message.name"] should not be nil`)
	}
	if *m["message.name"] != "messages/*" {
		t.Fatal(`m["message.name"] should be "messages/*"`)
	}
}

func TestHTTPTemplateClientUsesBuildPathAndProtoJSONHeaders(t *testing.T) {
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
		`path, err := http.BuildPath(pattern, in, http.WithQueryParams())`,
		`path, err := http.BuildPath(pattern, in)`,
		`if err != nil`,
		`http.Accept("application/protojson")`,
		`http.ContentType("application/protojson")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "binding.") {
		t.Fatalf("generated template should not reference binding package:\n%s", got)
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
				Name:                "UploadHello",
				OriginalName:        "UploadHello",
				Request:             "UploadHelloRequest",
				Reply:               "UploadHelloReply",
				Path:                "/helloworld/upload",
				PathTemplate:        "/helloworld/upload",
				Method:              "POST",
				HasBody:             true,
				BodyField:           "body",
				BodyQueryName:       "body",
				BodyGetter:          ".GetBody()",
				BodyType:            "*HTTPBody",
				BodyAssignment:      "in.SetBody(body)",
				BodyHTTPBody:        true,
				BodyMessage:         true,
				BodyProtoJSON:       true,
				ResponseBodyGetter:  ".GetBody()",
				ResponseBodyType:    "*HTTPBody",
				ResponseAssignment:  "out.SetBody(responseBody)",
				ResponseBodyMessage: true,
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
		`path, err := http.BuildPath(x.pattern, m, http.WithQueryParams())`,
		`stream, err := x.cc.WebSocket(x.ctx, path, opts...)`,
		`http.ContentType("application/protojson")`,
		`return &Greeter_ChatHelloHTTPClient{ctx: ctx, cc: c.cc, pattern: pattern, opts: opts}, nil`,
		`http.ContentType(http.BodyContentType(in.GetBody()))`,
		`http.WithOmitFields("body")`,
		`return ctx.Result(200, reply.GetBody())`,
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

	cmd := exec.Command(
		"protoc",
		"-I", "testdata",
		"-I", protocInclude,
		"-I", filepath.Join(protobufDir, "src"),
		"-I", filepath.Join(root, "third_party"),
		"--go_out="+tmp,
		"--go_opt=module=opaque.test",
		"--go-http_out="+tmp,
		"--go-http_opt=module=opaque.test",
		"opaque/opaque.proto",
		"open/open.proto",
	)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}

	goMod := fmt.Sprintf("module opaque.test\n\ngo 1.26.0\n\nrequire github.com/openkratos/kratos v0.0.0\n\nreplace github.com/openkratos/kratos => %s\n", root)
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, tmp, "go", "test", "-mod=mod", "./...")
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
