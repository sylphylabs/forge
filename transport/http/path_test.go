package http

import (
	"errors"
	"testing"

	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/openkratos/kratos/internal/testdata/binding"
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
			request:      &binding.HelloRequest{Name: "kratos"},
			want:         "/helloworld/kratos",
		},
		{
			name:         "query",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "kratos", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/helloworld/kratos?sub.naming=go",
		},
		{
			name:         "resource name",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			request:      &binding.HelloRequest{Name: "publishers/go/books/kratos"},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/publishers/go/books/kratos",
		},
		{
			name:         "omit body field query params",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "kratos", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams(), WithOmitFields("sub")},
			want:         "/helloworld/kratos",
		},
		{
			name:         "escape single segment",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "kratos/admin?enabled=true#fragment"},
			want:         "/helloworld/kratos%2Fadmin%3Fenabled%3Dtrue%23fragment",
		},
		{
			name:         "preserve multi segment wildcard",
			pathTemplate: "/v1/{name=**}",
			request:      &binding.HelloRequest{Name: "publishers/go lang/books/kratos"},
			want:         "/v1/publishers/go%20lang/books/kratos",
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
				value := "open kratos"
				return &binding.HelloRequest{OptString: &value}
			}(),
			opts: []BuildPathOption{WithQueryParams()},
			want: "/v1/open%20kratos",
		},
		{
			name:         "nested proto text field name",
			pathTemplate: "/v1/{sub.name}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "openkratos"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/openkratos",
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
		opts         []BuildPathOption
		want         string
	}{
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "2233!!!!"}},
			want:         "http://helloworld.Greeter/helloworld/test/sub/2233!!!!",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      nil,
			want:         "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/hello",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.name.cc}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{test_repeated}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}, TestRepeated: []string{"123", "456"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/123",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "5566!!!"}},
			want:         "http://helloworld.Greeter/helloworld/test/sub/5566!!!",
		},
		{
			pathTemplate: "/helloworld/sub",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "2233!!!"}},
			want:         "http://helloworld.Greeter/helloworld/sub",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.name}",
			request:      &binding.HelloRequest{Name: "test"},
			want:         "http://helloworld.Greeter/helloworld/test/sub/",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub",
			request:      &binding.HelloRequest{Name: "go", Sub: &binding.Sub{Name: "kratos"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/go/sub?sub.naming=kratos",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name=publishers/*/books/*}/sub",
			request:      &binding.HelloRequest{Name: "publishers/go/books/kratos", Sub: &binding.Sub{Name: "kratos"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/publishers/go/books/kratos/sub?sub.naming=kratos",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name=**}/sub",
			request:      &binding.HelloRequest{Name: "publishers/go/books/kratos", Sub: &binding.Sub{Name: "kratos"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/publishers/go/books/kratos/sub?sub.naming=kratos",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{sub.naming=publishers/*}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "publishers/kratos"}},
			want:         "http://helloworld.Greeter/helloworld/publishers/kratos",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub/{sub.naming}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "sub.naming"}}},
			want:         "http://helloworld.Greeter/helloworld/sub/kratos?updateMask=name,sub.naming",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/sub/[kratos]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{name}]/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/[test]/sub/[kratos]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{}]/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/[{}]/sub/[kratos]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{}]/sub/[{sub.naming}]/{[]}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/[{}]/sub/[kratos]/{[]}",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{[sub]}/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/{[sub]}/[kratos]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{[name]}/[{sub.naming}]",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/{[name]}/[kratos]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/[]/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "kratos"}},
			want:         "http://helloworld.Greeter/helloworld/{}/[]/[kratos]",
		},
	}

	for _, test := range tests {
		if _, err := BuildPath(test.pathTemplate, test.request, test.opts...); err == nil {
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
				Sub:  &binding.Sub{Name: "kratos"},
			},
		},
		{
			name:         "NoParamsWithQuery",
			pathTemplate: "http://helloworld.Greeter/helloworld/sub",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "kratos"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "WithParams",
			pathTemplate: "/helloworld/{name}/sub/{sub.naming}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "kratos"},
			},
		},
		{
			name:         "WithParamsAndQuery",
			pathTemplate: "/helloworld/{name}/sub/{sub.naming}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "kratos"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = BuildPath(bm.pathTemplate, bm.msg, bm.opts...)
			}
		})
	}
}
