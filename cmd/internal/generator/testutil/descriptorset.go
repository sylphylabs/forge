package testutil

import (
	"os"
	"path/filepath"
	"testing"

	// Registers google/api/annotations.proto and its dependencies in the
	// global file registry so their descriptors can be exported below.
	_ "google.golang.org/genproto/googleapis/api/annotations"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// AnnotationsDescriptorSet writes a FileDescriptorSet containing
// google/api/annotations.proto and its transitive dependencies and returns its
// path. The descriptors come from the genproto module linked into the test
// binary, so protoc resolves the google.api.http extension through
// --descriptor_set_in at the version go.mod pins.
func AnnotationsDescriptorSet(t *testing.T) string {
	t.Helper()
	set := new(descriptorpb.FileDescriptorSet)
	seen := make(map[string]bool)
	var add func(path string)
	add = func(path string) {
		if seen[path] {
			return
		}
		seen[path] = true
		fd, err := protoregistry.GlobalFiles.FindFileByPath(path)
		if err != nil {
			t.Fatalf("find descriptor %s: %v", path, err)
		}
		fdp := protodesc.ToFileDescriptorProto(fd)
		for _, dep := range fdp.GetDependency() {
			add(dep)
		}
		set.File = append(set.File, fdp)
	}
	add("google/api/annotations.proto")
	data, err := proto.Marshal(set)
	if err != nil {
		t.Fatalf("marshal descriptor set: %v", err)
	}
	path := filepath.Join(t.TempDir(), "annotations.binpb")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write descriptor set: %v", err)
	}
	return path
}
