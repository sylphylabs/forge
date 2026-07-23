package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
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
	apiRoot := filepath.Join(filepath.Dir(root), "OpenKratos-api")
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
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	runCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")
	runCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-grpc"), "google.golang.org/grpc/cmd/protoc-gen-go-grpc")
	runCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-openkratos"), ".")

	args := []string{
		"-I", "testdata",
		"-I", protocInclude,
		"-I", filepath.Join(root, "third_party"),
		"--go_out=" + out,
		"--go_opt=module=middleware.test",
		"--go-grpc_out=" + out,
		"--go-grpc_opt=module=middleware.test",
		"--go-openkratos_out=" + out,
		"--go-openkratos_opt=module=middleware.test,grpc=true",
		"middleware/service.proto",
	}
	cmd := exec.Command("protoc", args...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}

	generatedPath := filepath.Join(out, "api", "service_openkratos.pb.go")
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
	github.com/openkratos/api v0.0.0
	github.com/openkratos/kratos v0.0.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

replace github.com/openkratos/api => %s

replace github.com/openkratos/kratos => %s
`, apiRoot, root)
	if err := os.WriteFile(filepath.Join(out, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommand(t, out, "go", "test", "-mod=mod", "./...")
}
