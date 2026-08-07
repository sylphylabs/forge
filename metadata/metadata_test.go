package metadata

import (
	"context"
	"reflect"
	"testing"
)

func TestNew(t *testing.T) {
	type args struct {
		mds []map[string][]string
	}
	tests := []struct {
		name string
		args args
		want Metadata
	}{
		{
			name: "hello",
			args: args{[]map[string][]string{{"hello": {"forge"}}, {"hello2": {"go-kratos"}}}},
			want: Metadata{"hello": {"forge"}, "hello2": {"go-kratos"}},
		},
		{
			name: "hi",
			args: args{[]map[string][]string{{"hi": {"forge"}}, {"hi2": {"go-kratos"}}}},
			want: Metadata{"hi": {"forge"}, "hi2": {"go-kratos"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New(tt.args.mds...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("New() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadata_Get(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name string
		m    Metadata
		args args
		want string
	}{
		{
			name: "forge",
			m:    Metadata{"forge": {"value"}, "env": {"dev"}},
			args: args{key: "forge"},
			want: "value",
		},
		{
			name: "env",
			m:    Metadata{"forge": {"value"}, "env": {"dev"}},
			args: args{key: "env"},
			want: "dev",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Get(tt.args.key); got != tt.want {
				t.Errorf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadata_Values(t *testing.T) {
	type args struct {
		key string
	}
	tests := []struct {
		name string
		m    Metadata
		args args
		want []string
	}{
		{
			name: "forge",
			m:    Metadata{"forge": {"value", "value2"}, "env": {"dev"}},
			args: args{key: "forge"},
			want: []string{"value", "value2"},
		},
		{
			name: "env",
			m:    Metadata{"forge": {"value", "value2"}, "env": {"dev"}},
			args: args{key: "env"},
			want: []string{"dev"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Values(tt.args.key); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Get() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMetadata_Set(t *testing.T) {
	type args struct {
		key   string
		value string
	}
	tests := []struct {
		name string
		m    Metadata
		args args
		want Metadata
	}{
		{
			name: "forge",
			m:    Metadata{},
			args: args{key: "hello", value: "forge"},
			want: Metadata{"hello": {"forge"}},
		},
		{
			name: "env",
			m:    Metadata{"hello": {"forge"}},
			args: args{key: "env", value: "pro"},
			want: Metadata{"hello": {"forge"}, "env": {"pro"}},
		},
		{
			name: "empty",
			m:    Metadata{},
			args: args{key: "", value: ""},
			want: Metadata{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.m.Set(tt.args.key, tt.args.value)
			if !reflect.DeepEqual(tt.m, tt.want) {
				t.Errorf("Set() = %v, want %v", tt.m, tt.want)
			}
		})
	}
}

func TestMetadata_Add(t *testing.T) {
	type args struct {
		key   string
		value string
	}
	tests := []struct {
		name string
		m    Metadata
		args args
		want Metadata
	}{
		{
			name: "forge",
			m:    Metadata{},
			args: args{key: "hello", value: "forge"},
			want: Metadata{"hello": {"forge"}},
		},
		{
			name: "env",
			m:    Metadata{"hello": {"forge"}},
			args: args{key: "hello", value: "again"},
			want: Metadata{"hello": {"forge", "again"}},
		},
		{
			name: "empty",
			m:    Metadata{},
			args: args{key: "", value: ""},
			want: Metadata{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.m.Add(tt.args.key, tt.args.value)
			if !reflect.DeepEqual(tt.m, tt.want) {
				t.Errorf("Set() = %v, want %v", tt.m, tt.want)
			}
		})
	}
}

func TestClientContext(t *testing.T) {
	type args struct {
		ctx context.Context
		md  Metadata
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "forge",
			args: args{context.Background(), Metadata{"hello": {"forge"}, "forge": {"https://go-kratos.dev"}}},
		},
		{
			name: "hello",
			args: args{context.Background(), Metadata{"hello": {"forge"}, "hello2": {"https://go-kratos.dev"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewClientContext(tt.args.ctx, tt.args.md)
			m, ok := FromClientContext(ctx)
			if !ok {
				t.Errorf("FromClientContext() = %v, want %v", ok, true)
			}

			if !reflect.DeepEqual(m, tt.args.md) {
				t.Errorf("meta = %v, want %v", m, tt.args.md)
			}
		})
	}
}

func TestServerContext(t *testing.T) {
	type args struct {
		ctx context.Context
		md  Metadata
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "forge",
			args: args{context.Background(), Metadata{"hello": {"forge"}, "forge": {"https://go-kratos.dev"}}},
		},
		{
			name: "hello",
			args: args{context.Background(), Metadata{"hello": {"forge"}, "hello2": {"https://go-kratos.dev"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewServerContext(tt.args.ctx, tt.args.md)
			m, ok := FromServerContext(ctx)
			if !ok {
				t.Errorf("FromServerContext() = %v, want %v", ok, true)
			}

			if !reflect.DeepEqual(m, tt.args.md) {
				t.Errorf("meta = %v, want %v", m, tt.args.md)
			}
		})
	}
}

func TestAppendToClientContext(t *testing.T) {
	type args struct {
		md Metadata
		kv []string
	}
	tests := []struct {
		name string
		args args
		want Metadata
	}{
		{
			name: "forge",
			args: args{Metadata{}, []string{"hello", "forge", "env", "dev"}},
			want: Metadata{"hello": {"forge"}, "env": {"dev"}},
		},
		{
			name: "hello",
			args: args{Metadata{"hi": {"https://go-kratos.dev/"}}, []string{"hello", "forge", "env", "dev"}},
			want: Metadata{"hello": {"forge"}, "env": {"dev"}, "hi": {"https://go-kratos.dev/"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewClientContext(context.Background(), tt.args.md)
			ctx = AppendToClientContext(ctx, tt.args.kv...)
			md, ok := FromClientContext(ctx)
			if !ok {
				t.Errorf("FromServerContext() = %v, want %v", ok, true)
			}
			if !reflect.DeepEqual(md, tt.want) {
				t.Errorf("metadata = %v, want %v", md, tt.want)
			}
		})
	}
}

// nolint directives: sa5012
func TestAppendToClientContextThatPanics(t *testing.T) {
	kvs := []string{"hello", "forge", "env"}
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("append to client context singular kvs did not panic")
		}
	}()
	ctx := NewClientContext(context.Background(), Metadata{})
	ctx = AppendToClientContext(ctx, kvs...)
	md, ok := FromClientContext(ctx)
	if !ok {
		t.Errorf("FromServerContext() = %v, want %v", ok, true)
	}
	if !reflect.DeepEqual(md, Metadata{}) {
		t.Errorf("metadata = %v, want %v", md, Metadata{})
	}
}

func TestMergeToClientContext(t *testing.T) {
	type args struct {
		md       Metadata
		appendMd Metadata
	}
	tests := []struct {
		name string
		args args
		want Metadata
	}{
		{
			name: "forge",
			args: args{Metadata{}, Metadata{"hello": {"forge"}, "env": {"dev"}}},
			want: Metadata{"hello": {"forge"}, "env": {"dev"}},
		},
		{
			name: "hello",
			args: args{Metadata{"hi": {"https://go-kratos.dev/"}}, Metadata{"hello": {"forge"}, "env": {"dev"}}},
			want: Metadata{"hello": {"forge"}, "env": {"dev"}, "hi": {"https://go-kratos.dev/"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewClientContext(context.Background(), tt.args.md)
			ctx = MergeToClientContext(ctx, tt.args.appendMd)
			md, ok := FromClientContext(ctx)
			if !ok {
				t.Errorf("FromServerContext() = %v, want %v", ok, true)
			}
			if !reflect.DeepEqual(md, tt.want) {
				t.Errorf("metadata = %v, want %v", md, tt.want)
			}
		})
	}
}

func TestMetadata_Range(t *testing.T) {
	md := Metadata{"forge": {"forge"}, "https://go-kratos.dev/": {"https://go-kratos.dev/"}, "go-kratos": {"go-kratos"}}
	tmp := Metadata{}
	md.Range(func(k string, v []string) bool {
		if k == "https://go-kratos.dev/" || k == "forge" {
			tmp[k] = v
		}
		return true
	})
	if !reflect.DeepEqual(tmp, Metadata{"https://go-kratos.dev/": {"https://go-kratos.dev/"}, "forge": {"forge"}}) {
		t.Errorf("metadata = %v, want %v", tmp, Metadata{"https://go-kratos.dev/": {"https://go-kratos.dev/"}, "forge": {"forge"}})
	}
	tmp = Metadata{}
	md.Range(func(string, []string) bool {
		return false
	})
	if !reflect.DeepEqual(tmp, Metadata{}) {
		t.Errorf("metadata = %v, want %v", tmp, Metadata{})
	}
}

func TestMetadata_Clone(t *testing.T) {
	tests := []struct {
		name string
		m    Metadata
		want Metadata
	}{
		{
			name: "forge",
			m:    Metadata{"forge": {"forge"}, "https://go-kratos.dev/": {"https://go-kratos.dev/"}, "go-kratos": {"go-kratos"}},
			want: Metadata{"forge": {"forge"}, "https://go-kratos.dev/": {"https://go-kratos.dev/"}, "go-kratos": {"go-kratos"}},
		},
		{
			name: "go",
			m:    Metadata{"language": {"golang"}},
			want: Metadata{"language": {"golang"}},
		},
		{
			name: "plan9",
			m:    Metadata{"k0": []string{}, "k1": nil},
			want: Metadata{"k0": []string{}, "k1": nil},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.m.Clone()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Clone() = %v, want %v", got, tt.want)
			}
			got["forge"] = []string{"go"}
			if reflect.DeepEqual(got, tt.want) {
				t.Errorf("want got != want got %v want %v", got, tt.want)
			}
		})
	}
}

// TestMetadata_CloneDeepCopy tests that Clone creates a deep copy of metadata,
// so modifications to the original metadata's slices don't affect the cloned one.
func TestMetadata_CloneDeepCopy(t *testing.T) {
	original := Metadata{
		"test-key":   {"value1", "value2", "value3"},
		"single-key": {"single-value"},
	}

	cloned := original.Clone()

	// Test 1: Modify an element in the original metadata's slice
	{
		original["test-key"][1] = "modified-value"

		// Verify that the cloned metadata's slice is not affected
		if cloned["test-key"][1] != "value2" {
			t.Errorf("Clone() modify leaked: original=%v, cloned=%v", original["test-key"], cloned["test-key"])
		}
	}

	// Test 2: Append to the original metadata's slice
	{
		original["test-key"] = append(original["test-key"], "new-value")
		if len(cloned["test-key"]) != 3 {
			t.Errorf("Clone() append leaked: original len=%d, cloned len=%d", len(original["test-key"]), len(cloned["test-key"]))
		}
		expected := []string{"value1", "value2", "value3"}
		if !reflect.DeepEqual(cloned["test-key"], expected) {
			t.Errorf("Clone() append values: got=%v, want=%v", cloned["test-key"], expected)
		}
	}

	// Test 3: Replace the entire slice in the original metadata
	{
		original["single-key"] = []string{"replaced-value"}
		if cloned["single-key"][0] != "single-value" {
			t.Errorf("Clone() replace leaked: original=%v, cloned=%v", original["single-key"], cloned["single-key"])
		}
	}
}
