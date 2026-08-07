package http

import (
	"errors"
	"sync"
	"testing"

	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/sylphylabs/forge/internal/testdata/binding"
)

func TestBuildPath(t *testing.T) {
	tests := []struct {
		name         string
		pathTemplate string
		request      *binding.HelloRequest
		opts         []BuildPathOption
		want         string
		wantErr      bool
	}{
		{
			name:         "path",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "forge"},
			want:         "/helloworld/forge",
		},
		{
			name:         "query",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "forge", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/helloworld/forge?sub.naming=go",
		},
		{
			name:         "resource name",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			request:      &binding.HelloRequest{Name: "publishers/go/books/forge"},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/publishers/go/books/forge",
		},
		{
			name:         "omit body field query params",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "forge", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams(), WithOmitFields("sub")},
			want:         "/helloworld/forge",
		},
		{
			name:         "escape single segment",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "forge/admin?enabled=true#fragment"},
			want:         "/helloworld/forge%2Fadmin%3Fenabled%3Dtrue%23fragment",
		},
		{
			name:         "preserve multi segment wildcard",
			pathTemplate: "/v1/{name=**}",
			request:      &binding.HelloRequest{Name: "publishers/go lang/books/forge"},
			want:         "/v1/publishers/go%20lang/books/forge",
		},
		{
			name:         "reject mismatched resource structure safely",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			request:      &binding.HelloRequest{Name: "organizations/acme/secrets/root"},
			wantErr:      true,
		},
		{
			name:         "proto text field name",
			pathTemplate: "/v1/{opt_string}",
			request: func() *binding.HelloRequest {
				value := "open forge"
				return &binding.HelloRequest{OptString: &value}
			}(),
			opts: []BuildPathOption{WithQueryParams()},
			want: "/v1/open%20forge",
		},
		{
			name:         "nested proto text field name",
			pathTemplate: "/v1/{sub.name}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "forge"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/forge",
		},
		{
			name:         "field mask query",
			pathTemplate: "/v1/{name}",
			request:      &binding.HelloRequest{Name: "books/1", UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "sub.naming"}}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/books%2F1?updateMask=name%2Csub.naming",
		},
		{
			name:         "int64 path lexical form",
			pathTemplate: "/v1/{opt_int64}",
			request: func() *binding.HelloRequest {
				value := int64(9223372036854775807)
				return &binding.HelloRequest{OptInt64: &value}
			}(),
			want: "/v1/9223372036854775807",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildPath(tt.pathTemplate, tt.request, tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("expected %s got %s", tt.want, got)
			}
		})
	}
}

func TestCompiledPath(t *testing.T) {
	tests := []struct {
		name         string
		pathTemplate string
		request      *binding.HelloRequest
		opts         []BuildPathOption
	}{
		{
			name:         "path",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "open forge"},
		},
		{
			name:         "nested path and query",
			pathTemplate: "/helloworld/{name}/sub/{sub.name}",
			request: &binding.HelloRequest{
				Name:       "open forge",
				Sub:        &binding.Sub{Name: "nested"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "omit query field",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "forge", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams(), WithOmitFields("sub")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compiled, err := CompilePath(tt.pathTemplate, new(binding.HelloRequest), tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			got, err := compiled.Build(tt.request)
			if err != nil {
				t.Fatal(err)
			}
			want, err := BuildPath(tt.pathTemplate, tt.request, tt.opts...)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("Build() = %q, want %q", got, want)
			}
		})
	}
}

func TestCompiledPathRejectsInvalidUse(t *testing.T) {
	if _, err := CompilePath("/v1/{name}", (*binding.HelloRequest)(nil)); err == nil {
		t.Fatal("CompilePath() accepted a nil prototype")
	}
	compiled, err := CompilePath("/v1/{name}", new(binding.HelloRequest))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiled.Build(wrapperspb.String("forge")); err == nil {
		t.Fatal("Build() accepted a different message type")
	}
	if _, err := (*CompiledPath)(nil).Build(new(binding.HelloRequest)); err == nil {
		t.Fatal("nil CompiledPath.Build() succeeded")
	}
}

func TestMustCompilePathConcurrent(t *testing.T) {
	compiled := MustCompilePath(
		"/helloworld/{name}/sub/{sub.name}",
		new(binding.HelloRequest),
		WithQueryParams(),
	)
	request := &binding.HelloRequest{Name: "forge", Sub: &binding.Sub{Name: "go"}}
	const want = "/helloworld/forge/sub/go"

	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			got, err := compiled.Build(request)
			if err != nil {
				t.Errorf("Build() error = %v", err)
				return
			}
			if got != want {
				t.Errorf("Build() = %q, want %q", got, want)
			}
		})
	}
	wg.Wait()
}

func TestMustCompilePathPanicsOnInvalidTemplate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustCompilePath() did not panic")
		}
	}()
	MustCompilePath("v1/{name}", new(binding.HelloRequest))
}

func TestBuildPathErrors(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		request *binding.HelloRequest
		wantIs  error
	}{
		{name: "invalid template", pattern: "v1/{name}", request: &binding.HelloRequest{Name: "x"}},
		{name: "missing field", pattern: "/v1/{missing}", request: &binding.HelloRequest{}},
		{name: "unset optional", pattern: "/v1/{opt_string}", request: &binding.HelloRequest{}},
		{name: "repeated field", pattern: "/v1/{test_repeated}", request: &binding.HelloRequest{TestRepeated: []string{"a"}}},
		{name: "message field", pattern: "/v1/{sub}", request: &binding.HelloRequest{Sub: &binding.Sub{Name: "a"}}},
		{name: "nil request", pattern: "/v1/{name}"},
		{name: "unbound wildcard", pattern: "/v1/*", request: &binding.HelloRequest{}, wantIs: ErrUnboundPathWildcard},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildPath(tt.pattern, tt.request)
			if err == nil {
				t.Fatal("expected an error")
			}
			if tt.wantIs != nil && !errors.Is(err, tt.wantIs) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.wantIs)
			}
		})
	}
}

func TestBuildPathRejectsAbsoluteTemplates(t *testing.T) {
	tests := []struct {
		pathTemplate string
		request      *binding.HelloRequest
	}{
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "2233!!!!"}},
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.name.cc}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{test_repeated}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}, TestRepeated: []string{"123", "456"}},
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "forge"}},
		},
	}

	for _, test := range tests {
		if _, err := BuildPath(test.pathTemplate, test.request); err == nil {
			t.Errorf("BuildPath(%q) succeeded", test.pathTemplate)
		}
	}
}

func BenchmarkBuildPath(b *testing.B) {
	benchmarks := []struct {
		name         string
		pathTemplate string
		msg          *binding.HelloRequest
		opts         []BuildPathOption
	}{
		{
			name:         "NoParams",
			pathTemplate: "/helloworld/sub",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "forge"},
			},
		},
		{
			name:         "NoParamsWithQuery",
			pathTemplate: "/helloworld/sub",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "forge"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "WithParams",
			pathTemplate: "/helloworld/{name}/sub/{sub.name}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "forge"},
			},
		},
		{
			name:         "WithParamsAndQuery",
			pathTemplate: "/helloworld/{name}/sub/{sub.name}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "forge"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "AIPResourceName",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			msg:          &binding.HelloRequest{Name: "publishers/acme/books/42"},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = BuildPath(bm.pathTemplate, bm.msg, bm.opts...)
			}
		})
	}
}

func BenchmarkCompiledPath(b *testing.B) {
	benchmarks := []struct {
		name         string
		pathTemplate string
		msg          *binding.HelloRequest
		opts         []BuildPathOption
	}{
		{
			name:         "NoParams",
			pathTemplate: "/helloworld/sub",
			msg:          &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "forge"}},
		},
		{
			name:         "NoParamsWithQuery",
			pathTemplate: "/helloworld/sub",
			msg: &binding.HelloRequest{
				Name:       "test",
				Sub:        &binding.Sub{Name: "forge"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "sub.name"}},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "WithParams",
			pathTemplate: "/helloworld/{name}/sub/{sub.name}",
			msg:          &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "forge"}},
		},
		{
			name:         "WithParamsAndQuery",
			pathTemplate: "/helloworld/{name}/sub/{sub.name}",
			msg: &binding.HelloRequest{
				Name:       "test",
				Sub:        &binding.Sub{Name: "forge"},
				UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "sub.name"}},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "AIPResourceName",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			msg:          &binding.HelloRequest{Name: "publishers/acme/books/42"},
		},
	}

	for _, bm := range benchmarks {
		compiled := MustCompilePath(bm.pathTemplate, new(binding.HelloRequest), bm.opts...)
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = compiled.Build(bm.msg)
			}
		})
	}
}
