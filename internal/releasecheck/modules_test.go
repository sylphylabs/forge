package releasecheck

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type manifest struct {
	SchemaVersion int           `json:"schemaVersion"`
	Modules       []moduleEntry `json:"modules"`
}

type moduleEntry struct {
	Path      string   `json:"path"`
	Module    string   `json:"module"`
	Kind      string   `json:"kind"`
	Support   string   `json:"support"`
	Owner     string   `json:"owner"`
	TagPrefix string   `json:"tagPrefix"`
	DependsOn []string `json:"dependsOn"`
}

type goMod struct {
	Module struct {
		Path string
	}
	Require []struct {
		Path string
	}
	Replace []struct {
		Old struct {
			Path string
		}
		New struct {
			Path string
		}
	}
}

func TestModuleManifest(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "modules.json"))
	if err != nil {
		t.Fatal(err)
	}

	var inventory manifest
	if err := json.Unmarshal(data, &inventory); err != nil {
		t.Fatalf("decode modules.json: %v", err)
	}
	if inventory.SchemaVersion != 1 {
		t.Fatalf("schemaVersion = %d, want 1", inventory.SchemaVersion)
	}

	discovered := discoverModules(t, root)
	validateManifest(t, root, inventory.Modules, discovered)
}

func TestLegacyErrorSchemasRemoved(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{
		"errors/errors.proto",
		"cmd/protoc-gen-go-errors/errors/errors.proto",
		"third_party/errors/errors.proto",
	} {
		_, err := os.Stat(filepath.Join(root, name))
		if err == nil {
			t.Errorf("legacy public error schema still exists: %s", name)
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %s: %v", name, err)
		}
	}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root {
			switch entry.Name() {
			case ".git", "output", "vendor":
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || filepath.Ext(path) != ".proto" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		source := string(data)
		for _, legacy := range []string{
			`"errors/errors.proto"`,
			"(errors.default_code)",
			"(errors.code)",
		} {
			if strings.Contains(source, legacy) {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				t.Errorf("%s contains legacy error contract reference %q", filepath.ToSlash(rel), legacy)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBufModulesRemoved(t *testing.T) {
	root := repositoryRoot(t)
	for _, name := range []string{"buf.yaml", "buf.gen.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, legacy := range []string{
			"buf.build/kratos/apis",
			"buf.build/go-kratos/protoc-gen-go-errors",
		} {
			if strings.Contains(string(data), legacy) {
				t.Errorf("%s contains legacy Buf module %q", name, legacy)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "third_party", "buf.yaml")); err == nil {
		t.Error("legacy third_party/buf.yaml still exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("stat third_party/buf.yaml: %v", err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate releasecheck source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func discoverModules(t *testing.T, root string) []string {
	t.Helper()
	var modules []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && path != root {
			switch entry.Name() {
			case ".git", "output", "third_party", "vendor":
				return filepath.SkipDir
			}
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		dir, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		modules = append(modules, filepath.ToSlash(dir))
		return nil
	})
	if err != nil {
		t.Fatalf("discover modules: %v", err)
	}
	slices.Sort(modules)
	return modules
}

func validateManifest(t *testing.T, root string, entries []moduleEntry, discovered []string) {
	t.Helper()
	paths := make([]string, 0, len(entries))
	byModule := make(map[string]moduleEntry, len(entries))
	seenPaths := make(map[string]bool, len(entries))
	seenTags := make(map[string]bool, len(entries))

	for _, entry := range entries {
		if entry.Path == "" || filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path {
			t.Errorf("invalid module path %q", entry.Path)
		}
		if seenPaths[entry.Path] {
			t.Errorf("duplicate module path %q", entry.Path)
		}
		if _, ok := byModule[entry.Module]; ok {
			t.Errorf("duplicate module identity %q", entry.Module)
		}
		if seenTags[entry.TagPrefix] {
			t.Errorf("duplicate tag prefix %q", entry.TagPrefix)
		}
		seenPaths[entry.Path] = true
		seenTags[entry.TagPrefix] = true
		byModule[entry.Module] = entry
		paths = append(paths, entry.Path)

		wantTagPrefix := entry.Path
		if entry.Path == "." {
			wantTagPrefix = ""
		}
		if entry.TagPrefix != wantTagPrefix {
			t.Errorf("module %q tagPrefix = %q, want %q", entry.Path, entry.TagPrefix, wantTagPrefix)
		}
		if entry.Owner == "" {
			t.Errorf("module %q has no owner", entry.Path)
		}
		if !slices.Contains([]string{"core", "tool", "integration"}, entry.Kind) {
			t.Errorf("module %q has invalid kind %q", entry.Path, entry.Kind)
		}
		if !slices.Contains([]string{"core", "official", "community"}, entry.Support) {
			t.Errorf("module %q has invalid support %q", entry.Path, entry.Support)
		}
	}

	slices.Sort(paths)
	if !slices.Equal(paths, discovered) {
		t.Fatalf("manifest module paths = %v, discovered %v", paths, discovered)
	}

	for _, entry := range entries {
		mod := readGoMod(t, root, entry.Path)
		if mod.Module.Path != entry.Module {
			t.Errorf("%s/go.mod module = %q, want %q", entry.Path, mod.Module.Path, entry.Module)
		}

		var dependencies []string
		for _, requirement := range mod.Require {
			if _, ok := byModule[requirement.Path]; ok {
				dependencies = append(dependencies, requirement.Path)
			}
		}
		slices.Sort(dependencies)
		wantDependencies := slices.Clone(entry.DependsOn)
		slices.Sort(wantDependencies)
		if !slices.Equal(dependencies, wantDependencies) {
			t.Errorf("module %q internal dependencies = %v, want %v", entry.Module, dependencies, wantDependencies)
		}

		replacements := make(map[string]bool, len(mod.Replace))
		for _, replacement := range mod.Replace {
			target, ok := byModule[replacement.Old.Path]
			if !ok {
				continue
			}
			replacements[replacement.Old.Path] = true
			if !slices.Contains(dependencies, replacement.Old.Path) {
				t.Errorf("module %q replaces undeclared internal dependency %q", entry.Module, replacement.Old.Path)
			}
			if filepath.IsAbs(replacement.New.Path) {
				t.Errorf("module %q has machine-specific replace for %q", entry.Module, replacement.Old.Path)
				continue
			}
			got, err := filepath.Abs(filepath.Join(root, entry.Path, replacement.New.Path))
			if err != nil {
				t.Errorf("resolve replace in %q: %v", entry.Module, err)
				continue
			}
			want := filepath.Join(root, target.Path)
			if filepath.Clean(got) != filepath.Clean(want) {
				t.Errorf("module %q replace %q resolves to %q, want %q", entry.Module, replacement.Old.Path, got, want)
			}
		}
		for _, dependency := range dependencies {
			if !replacements[dependency] {
				t.Errorf("module %q has no local replace for internal dependency %q", entry.Module, dependency)
			}
		}
	}

	validateAcyclic(t, entries, byModule)
}

func readGoMod(t *testing.T, root, path string) goMod {
	t.Helper()
	filename := filepath.Join(root, path, "go.mod")
	command := exec.Command("go", "mod", "edit", "-json", "-modfile="+filename)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	var mod goMod
	if err := json.Unmarshal(output, &mod); err != nil {
		t.Fatalf("decode %s: %v", filename, err)
	}
	return mod
}

func validateAcyclic(t *testing.T, entries []moduleEntry, byModule map[string]moduleEntry) {
	t.Helper()
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(entries))
	var visit func(string, []string)
	visit = func(module string, stack []string) {
		switch state[module] {
		case visiting:
			t.Errorf("module dependency cycle: %s", strings.Join(append(stack, module), " -> "))
			return
		case visited:
			return
		}
		state[module] = visiting
		entry, ok := byModule[module]
		if !ok {
			t.Errorf("unknown internal dependency %q", module)
			return
		}
		for _, dependency := range entry.DependsOn {
			visit(dependency, append(stack, module))
		}
		state[module] = visited
	}
	for _, entry := range entries {
		visit(entry.Module, nil)
	}
}
