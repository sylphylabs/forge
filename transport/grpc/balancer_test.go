package grpc

import (
	"context"
	"reflect"
	"testing"

	"google.golang.org/grpc/balancer"
	"google.golang.org/grpc/balancer/base"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/resolver"

	"github.com/sylphylabs/forge/selector"
)

type testSubConn struct{ balancer.SubConn }

func TestBalancerBuildUsesGlobalSelector(t *testing.T) {
	conn := testSubConn{}
	picker := (&balancerBuilder{}).Build(base.PickerBuildInfo{
		ReadySCs: map[balancer.SubConn]base.SubConnInfo{
			conn: {Address: resolver.Address{Addr: "127.0.0.1:9000"}},
		},
	})

	result, err := picker.Pick(balancer.PickInfo{Ctx: t.Context()})
	if err != nil {
		t.Fatal(err)
	}
	if result.SubConn != conn {
		t.Fatalf("picked SubConn %v, want %v", result.SubConn, conn)
	}
	result.Done(balancer.DoneInfo{})
}

func TestTrailer(t *testing.T) {
	trailer := Trailer(metadata.New(map[string]string{"a": "b"}))
	if !reflect.DeepEqual("b", trailer.Get("a")) {
		t.Errorf("expect %v, got %v", "b", trailer.Get("a"))
	}
	if !reflect.DeepEqual("", trailer.Get("notfound")) {
		t.Errorf("expect %v, got %v", "", trailer.Get("notfound"))
	}
}

func TestFilters(t *testing.T) {
	o := &clientOptions{}

	WithNodeFilter(func(_ context.Context, nodes []selector.Node) []selector.Node {
		return nodes
	})(o)
	if !reflect.DeepEqual(1, len(o.filters)) {
		t.Errorf("expect %v, got %v", 1, len(o.filters))
	}
}
