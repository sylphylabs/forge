package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sylphylabs/forge/cmd/internal/generator/testutil"
)

func TestGeneratedMiddlewareCompilesAndRuns(t *testing.T) {
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
	apiRoot := filepath.Join(root, "api")
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
	out := filepath.Join(tmp, "consumer")
	if err = os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-grpc"), "google.golang.org/grpc/cmd/protoc-gen-go-grpc")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-http"), "../protoc-gen-go-http")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-middleware"), ".")

	args := []string{
		"-I", "testdata",
		"-I", protocInclude,
		"--descriptor_set_in=" + testutil.AnnotationsDescriptorSet(t),
		"--go_out=" + out,
		"--go_opt=module=middleware.test",
		"--go-grpc_out=" + out,
		"--go-grpc_opt=module=middleware.test",
		"--go-http_out=" + out,
		"--go-http_opt=module=middleware.test",
		"--go-middleware_out=" + out,
		"--go-middleware_opt=module=middleware.test,http=annotated,grpc=true",
		"middleware/service.proto",
	}
	cmd := exec.Command("protoc", args...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}

	generatedPath := filepath.Join(out, "api", "service_middleware.pb.go")
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"type DocumentServiceMiddleware struct",
		"GetDocument     []middleware.UnaryMiddleware",
		"WatchDocuments  []middleware.StreamMiddleware",
		"func WrapDocumentServiceHTTPServer",
		"func WrapDocumentServiceGRPCServer",
		"middleware.ComposeUnary",
		"middleware.ComposeStream",
	} {
		if !bytes.Contains(generated, []byte(want)) {
			t.Fatalf("generated middleware missing %q:\n%s", want, generated)
		}
	}
	for _, forbidden := range []string{"proto.GetExtension", "selector.", "sync.Once", "reflect."} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated middleware contains request-time mechanism %q", forbidden)
		}
	}

	consumerTest, err := os.ReadFile(filepath.Join("testdata", "middleware", "consumer_test.gotxt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "api", "service_middleware_test.go"), consumerTest, 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf(`module middleware.test

go 1.27rc2

require (
	github.com/sylphylabs/forge/api v0.0.0
	github.com/sylphylabs/forge v0.0.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/sylphylabs/forge/api => %s

replace github.com/sylphylabs/forge => %s
`, apiRoot, root)
	if err := os.WriteFile(filepath.Join(out, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.RunCommand(t, out, "go", "test", "-mod=mod", "./...")
}

func TestGeneratedMiddlewareRejectsIdentifierCollision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping protoc integration test in short mode")
	}
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skipf("protoc is not installed: %v", err)
	}

	tmp := t.TempDir()
	plugin := filepath.Join(tmp, "protoc-gen-go-middleware")
	testutil.RunCommand(t, ".", "go", "build", "-o", plugin, ".")
	cmd := exec.Command(
		"protoc",
		"-I", "testdata",
		"--plugin=protoc-gen-go-middleware="+plugin,
		"--go-middleware_out="+tmp,
		"--go-middleware_opt=grpc=true",
		"middleware/collision.proto",
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("protoc unexpectedly succeeded:\n%s", output)
	}
	for _, want := range []string{
		`proto "middleware/collision.proto"`,
		`service middleware.collision.v1.Collision`,
		`identifier "CollisionMiddleware"`,
		`message middleware.collision.v1.CollisionMiddleware`,
	} {
		if !bytes.Contains(output, []byte(want)) {
			t.Fatalf("protoc error missing %q:\n%s", want, output)
		}
	}
}

func TestGeneratedMiddlewarePlanDoesNotRequireTransportWrappers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping protoc integration test in short mode")
	}
	if _, err := exec.LookPath("protoc"); err != nil {
		t.Skipf("protoc is not installed: %v", err)
	}

	tmp := t.TempDir()
	plugin := filepath.Join(tmp, "protoc-gen-go-middleware")
	testutil.RunCommand(t, ".", "go", "build", "-o", plugin, ".")
	cmd := exec.Command(
		"protoc",
		"-I", "testdata",
		"--descriptor_set_in="+testutil.AnnotationsDescriptorSet(t),
		"--plugin=protoc-gen-go-middleware="+plugin,
		"--go-middleware_out="+tmp,
		"--go-middleware_opt=paths=source_relative",
		"middleware/service.proto",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}
	generated, err := os.ReadFile(filepath.Join(tmp, "middleware", "service_middleware.pb.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(generated, []byte("type DocumentServiceMiddleware struct")) {
		t.Fatalf("generated plan is missing:\n%s", generated)
	}
	for _, forbidden := range []string{"WrapDocumentServiceHTTPServer", "WrapDocumentServiceGRPCServer"} {
		if bytes.Contains(generated, []byte(forbidden)) {
			t.Fatalf("generated plan unexpectedly contains %q:\n%s", forbidden, generated)
		}
	}
}

func TestHTTPMethodSetModesMustMatch(t *testing.T) {
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
	apiRoot := filepath.Join(root, "api")
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-http"), "../protoc-gen-go-http")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-middleware"), ".")

	tests := []struct {
		name           string
		httpOmitEmpty  bool
		middlewareMode string
		wantCompile    bool
	}{
		{name: modeAnnotated, httpOmitEmpty: true, middlewareMode: modeAnnotated, wantCompile: true},
		{name: modeAll, httpOmitEmpty: false, middlewareMode: modeAll, wantCompile: true},
		{name: "HTTP annotated middleware all", httpOmitEmpty: true, middlewareMode: modeAll},
		{name: "HTTP all middleware annotated", httpOmitEmpty: false, middlewareMode: modeAnnotated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(tmp, strings.ReplaceAll(test.name, " ", "_"))
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatal(err)
			}
			args := []string{
				"-I", "testdata",
				"--descriptor_set_in=" + testutil.AnnotationsDescriptorSet(t),
				"--go_out=" + out,
				"--go_opt=module=methodset.test",
				"--go-http_out=" + out,
				"--go-http_opt=module=methodset.test,omitempty=" + strconv.FormatBool(test.httpOmitEmpty),
				"--go-middleware_out=" + out,
				"--go-middleware_opt=module=methodset.test,http=" + test.middlewareMode,
				"methodset/service.proto",
			}
			cmd := exec.Command("protoc", args...)
			cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("protoc failed: %v\n%s", err, output)
			}
			consumer, err := os.ReadFile(filepath.Join("testdata", "methodset", "consumer_test.gotxt"))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(out, "api", "consumer_test.go"), consumer, 0o644); err != nil {
				t.Fatal(err)
			}
			goMod := fmt.Sprintf(`module methodset.test

go 1.27rc2

require (
	github.com/sylphylabs/forge/api v0.0.0
	github.com/sylphylabs/forge v0.0.0
	google.golang.org/protobuf v1.36.11
)

replace github.com/sylphylabs/forge/api => %s

replace github.com/sylphylabs/forge => %s
`, apiRoot, root)
			if err = os.WriteFile(filepath.Join(out, "go.mod"), []byte(goMod), 0o644); err != nil {
				t.Fatal(err)
			}
			cmd = exec.Command("go", "test", "-mod=mod", "./...")
			cmd.Dir = out
			output, err := cmd.CombinedOutput()
			if test.wantCompile && err != nil {
				t.Fatalf("matching method sets did not compile: %v\n%s", err, output)
			}
			if !test.wantCompile {
				if err == nil {
					t.Fatal("mismatched method sets unexpectedly compiled")
				}
				if !bytes.Contains(output, []byte("invalid array length")) {
					t.Fatalf("mismatch failure is not the compile-time method-set assertion:\n%s", output)
				}
			}
		})
	}
}
