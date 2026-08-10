package config

import (
	"reflect"
	"testing"
)

// A deep copy must preserve an empty slice.
//
// The gob round trip this replaced decoded `[]any{}` as nil, so a config value
// declared as `[]` silently became null once a second merge copied it — which
// any additional source or a config watch triggers.
func TestCloneMapPreservesEmptyCollections(t *testing.T) {
	src := map[string]any{
		"list":   []any{},
		"empty":  map[string]any{},
		"nested": map[string]any{"inner": []any{}},
	}
	cloned, err := cloneMap(src)
	if err != nil {
		t.Fatalf("cloneMap: %v", err)
	}
	if !reflect.DeepEqual(cloned, src) {
		t.Errorf("clone differs from source:\n got %#v\nwant %#v", cloned, src)
	}
}

// The copy must be independent: mutating it must not reach the original.
func TestCloneMapIsDeep(t *testing.T) {
	src := map[string]any{"nested": map[string]any{"n": 1}, "list": []any{1, 2}}
	cloned, err := cloneMap(src)
	if err != nil {
		t.Fatalf("cloneMap: %v", err)
	}
	cloned["nested"].(map[string]any)["n"] = 99
	cloned["list"].([]any)[0] = 99

	if src["nested"].(map[string]any)["n"] != 1 {
		t.Error("mutating the clone reached the source map")
	}
	if src["list"].([]any)[0] != 1 {
		t.Error("mutating the clone reached the source slice")
	}
}

// The value must survive repeated merges, which is the path that copies.
func TestEmptySliceSurvivesRepeatedMerge(t *testing.T) {
	opts := options{decoder: defaultDecoder, resolver: defaultResolver, merge: defaultMerge}
	r := newReader(opts)
	if err := r.Merge(&KeyValue{Key: "a", Value: []byte(`{"list": []}`), Format: "json"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Merge(&KeyValue{Key: "b", Value: []byte(`{"other": 1}`), Format: "json"}); err != nil {
		t.Fatal(err)
	}
	v, ok := r.Value("list")
	if !ok {
		t.Fatal("list disappeared")
	}
	if got := v.Load(); !reflect.DeepEqual(got, []any{}) {
		t.Errorf("list = %#v after a second merge, want an empty slice", got)
	}
}
