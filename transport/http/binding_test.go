package http

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	forgeerror "github.com/sylphylabs/forge/errors"
)

type (
	testBind struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	testBind2 struct {
		Age int `json:"age"`
	}
)

var benchmarkBindTarget testBind

func TestBindQuery(t *testing.T) {
	type args struct {
		vars   url.Values
		target any
	}

	tests := []struct {
		name string
		args args
		err  error
		want any
	}{
		{
			name: "test",
			args: args{
				vars:   map[string][]string{"name": {"forge"}, "url": {"https://go-kratos.dev/"}},
				target: &testBind{},
			},
			err:  nil,
			want: &testBind{"forge", "https://go-kratos.dev/"},
		},
		{
			name: "test1",
			args: args{
				vars:   map[string][]string{"age": {"forge"}, "url": {"https://go-kratos.dev/"}},
				target: &testBind2{},
			},
			err: forgeerror.BadRequest("CODEC", "Field Namespace:age ERROR:Invalid Integer Value 'forge' Type 'int' Namespace 'age'"),
		},
		{
			name: "test2",
			args: args{
				vars:   map[string][]string{"age": {"1"}, "url": {"https://go-kratos.dev/"}},
				target: &testBind2{},
			},
			err:  nil,
			want: &testBind2{Age: 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bindQuery(tt.args.vars, tt.args.target)
			if !forgeerror.Is(err, tt.err) {
				t.Fatalf("bindQuery() error = %v, err %v", err, tt.err)
			}
			if err == nil && !reflect.DeepEqual(tt.args.target, tt.want) {
				t.Errorf("bindQuery() target = %v, want %v", tt.args.target, tt.want)
			}
		})
	}
}

func TestDefaultRequestQueryEmpty(t *testing.T) {
	target := &testBind{Name: "unchanged"}
	req := &http.Request{URL: &url.URL{}}
	if err := DefaultRequestQuery(req, target); err != nil {
		t.Fatal(err)
	}
	if target.Name != "unchanged" {
		t.Fatalf("DefaultRequestQuery() changed target to %#v", target)
	}
}

func BenchmarkBindQuery(b *testing.B) {
	values := url.Values{
		"name": {"forge"},
		"url":  {"https://go-kratos.dev/"},
	}
	b.ReportAllocs()
	for b.Loop() {
		var target testBind
		if err := bindQuery(values, &target); err != nil {
			b.Fatal(err)
		}
		benchmarkBindTarget = target
	}
}

func BenchmarkDefaultRequestQueryEmpty(b *testing.B) {
	req := &http.Request{URL: &url.URL{}}
	var target testBind
	b.ReportAllocs()
	for b.Loop() {
		if err := DefaultRequestQuery(req, &target); err != nil {
			b.Fatal(err)
		}
	}
}

func TestBindForm(t *testing.T) {
	type args struct {
		req    *http.Request
		target any
	}

	tests := []struct {
		name string
		args args
		err  error
		want *testBind
	}{
		{
			name: "error not nil",
			args: args{
				req:    &http.Request{Method: http.MethodPost},
				target: &testBind{},
			},
			err:  errors.New("missing form body"),
			want: nil,
		},
		{
			name: "error is nil",
			args: args{
				req: &http.Request{
					Method: http.MethodPost,
					Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded; param=value"}},
					Body:   io.NopCloser(strings.NewReader("name=forge&url=https://go-kratos.dev/")),
				},
				target: &testBind{},
			},
			err:  nil,
			want: &testBind{"forge", "https://go-kratos.dev/"},
		},
		{
			name: "error BadRequest",
			args: args{
				req: &http.Request{
					Method: http.MethodPost,
					Header: http.Header{"Content-Type": {"application/x-www-form-urlencoded; param=value"}},
					Body:   io.NopCloser(strings.NewReader("age=a")),
				},
				target: &testBind2{},
			},
			err:  forgeerror.BadRequest("CODEC", "Field Namespace:age ERROR:Invalid Integer Value 'a' Type 'int' Namespace 'age'"),
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := bindForm(tt.args.req, tt.args.target)
			if !reflect.DeepEqual(err, tt.err) {
				t.Fatalf("bindForm() error = %v, err %v", err, tt.err)
			}
			if err == nil && !reflect.DeepEqual(tt.args.target, tt.want) {
				t.Errorf("bindForm() target = %v, want %v", tt.args.target, tt.want)
			}
		})
	}
}
