package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sylphylabs/forge/cmd/internal/generator/testutil"
)

// The generated file is only correct if it compiles against the live errors
// runtime and its sentinels behave: string assertions on the emitted source
// cannot see a reference to an identifier the runtime does not export. This
// test builds the real plugins, generates from a real proto, and go-tests the
// result in a consumer module wired to this repository.
func TestGeneratedErrorsCompileAndRun(t *testing.T) {
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
	out := filepath.Join(tmp, "consumer")
	if err = os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go"), "google.golang.org/protobuf/cmd/protoc-gen-go")
	testutil.RunCommand(t, ".", "go", "build", "-o", filepath.Join(bin, "protoc-gen-go-errors"), ".")

	args := []string{
		"-I", "testdata",
		"-I", filepath.Join(apiRoot, "proto"),
		"--go_out=" + out,
		"--go_opt=module=errors.test",
		"--go-errors_out=" + out,
		"--go-errors_opt=module=errors.test",
		"errors/service.proto",
	}
	cmd := exec.Command("protoc", args...)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("protoc failed: %v\n%s", err, output)
	}

	generated, err := os.ReadFile(filepath.Join(out, "api", "service_errors.pb.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"errors.SupportPackageIsVersion1",
		"var ErrNotFound = errors.MustDefine(",
		"var ErrBackendDown = errors.MustDefine(",
	} {
		if !bytes.Contains(generated, []byte(want)) {
			t.Fatalf("generated errors missing %q:\n%s", want, generated)
		}
	}

	consumerTest, err := os.ReadFile(filepath.Join("testdata", "errors", "consumer_test.gotxt"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(out, "api", "service_errors_test.go"), consumerTest, 0o644); err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf(`module errors.test

go 1.27rc3

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
	testutil.RunCommand(t, out, "go", "test", "-mod=mod", "./...")
}
