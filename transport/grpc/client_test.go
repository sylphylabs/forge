package grpc

import (
	"context"
	"crypto/tls"
	"reflect"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/openkratos/kratos/middleware"
	"github.com/openkratos/kratos/registry"
	"github.com/openkratos/kratos/transport"
)

func TestWithEndpoint(t *testing.T) {
	o := &clientOptions{}
	v := "abc"
	WithEndpoint(v)(o)
	if !reflect.DeepEqual(v, o.endpoint) {
		t.Errorf("expect %v but got %v", v, o.endpoint)
	}
}

func TestWithTimeout(t *testing.T) {
	o := &clientOptions{}
	v := time.Duration(123)
	WithTimeout(v)(o)
	if !reflect.DeepEqual(v, o.timeout) {
		t.Errorf("expect %v but got %v", v, o.timeout)
	}
}

func TestWithMiddleware(t *testing.T) {
	o := &clientOptions{}
	v := []middleware.UnaryMiddleware{
		func(middleware.UnaryHandler) middleware.UnaryHandler { return nil },
	}
	WithMiddleware(v...)(o)
	if !reflect.DeepEqual(v, o.middleware) {
		t.Errorf("expect %v but got %v", v, o.middleware)
	}
}

func TestWithStreamMiddleware(t *testing.T) {
	o := &clientOptions{}
	v := []middleware.UnaryMiddleware{
		func(middleware.UnaryHandler) middleware.UnaryHandler { return nil },
	}
	WithStreamMiddleware(v...)(o)
	if !reflect.DeepEqual(v, o.streamMiddleware) {
		t.Errorf("expect %v but got %v", v, o.streamInts)
	}
}

type mockRegistry struct{}

func (m *mockRegistry) GetService(_ context.Context, _ string) ([]*registry.ServiceInstance, error) {
	return nil, nil
}

func (m *mockRegistry) Watch(_ context.Context, _ string) (registry.Watcher, error) {
	return nil, nil
}

func TestWithDiscovery(t *testing.T) {
	o := &clientOptions{}
	v := &mockRegistry{}
	WithDiscovery(v)(o)
	if !reflect.DeepEqual(v, o.discovery) {
		t.Errorf("expect %v but got %v", v, o.discovery)
	}
}

func TestWithTLSConfig(t *testing.T) {
	o := &clientOptions{}
	v := &tls.Config{}
	WithTLSConfig(v)(o)
	if !reflect.DeepEqual(v, o.tlsConf) {
		t.Errorf("expect %v but got %v", v, o.tlsConf)
	}
}

func EmptyMiddleware() middleware.UnaryMiddleware {
	return func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (reply any, err error) {
			return handler(ctx, req)
		}
	}
}

func TestUnaryClientInterceptor(t *testing.T) {
	f := unaryClientInterceptor([]middleware.UnaryMiddleware{EmptyMiddleware()}, time.Duration(100), nil)
	req := &struct{}{}
	resp := &struct{}{}

	err := f(context.TODO(), "hello", req, resp, &grpc.ClientConn{},
		func(context.Context, string, any, any, *grpc.ClientConn, ...grpc.CallOption) error {
			return nil
		})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestUnaryClientInterceptorPropagatesAllHeaderValues(t *testing.T) {
	addHeaders := func(handler middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				t.Fatal("client transport missing from context")
			}
			tr.RequestHeader().Add("x-tag", "one")
			tr.RequestHeader().Add("x-tag", "two")
			return handler(ctx, req)
		}
	}

	interceptor := unaryClientInterceptor([]middleware.UnaryMiddleware{addHeaders}, 0, nil)
	err := interceptor(t.Context(), "hello", &struct{}{}, &struct{}{}, &grpc.ClientConn{},
		func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
			md, ok := metadata.FromOutgoingContext(ctx)
			if !ok {
				t.Fatal("outgoing metadata missing from context")
			}
			values := md.Get("x-tag")
			if len(values) != 2 || values[0] != "one" || values[1] != "two" {
				t.Fatalf("outgoing x-tag values = %q, want [one two]", values)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("unary interceptor returned an error: %v", err)
	}
}

func TestWithUnaryInterceptor(t *testing.T) {
	o := &clientOptions{}
	v := []grpc.UnaryClientInterceptor{
		func(context.Context, string, any, any, *grpc.ClientConn, grpc.UnaryInvoker, ...grpc.CallOption) error {
			return nil
		},
		func(context.Context, string, any, any, *grpc.ClientConn, grpc.UnaryInvoker, ...grpc.CallOption) error {
			return nil
		},
	}
	WithUnaryInterceptor(v...)(o)
	if !reflect.DeepEqual(v, o.ints) {
		t.Errorf("expect %v but got %v", v, o.ints)
	}
}

func TestWithOptions(t *testing.T) {
	o := &clientOptions{}
	v := []grpc.DialOption{
		grpc.EmptyDialOption{},
	}
	WithOptions(v...)(o)
	if !reflect.DeepEqual(v, o.grpcOpts) {
		t.Errorf("expect %v but got %v", v, o.grpcOpts)
	}
}

func TestWithHealthCheck(t *testing.T) {
	o := &clientOptions{
		healthCheckConfig: `,"healthCheckConfig":{"serviceName":""}`,
	}
	WithHealthCheck(false)(o)
	if !reflect.DeepEqual("", o.healthCheckConfig) {
		t.Errorf("expect %v but got %v", "", o.healthCheckConfig)
	}
}

func TestNewClientOptions(t *testing.T) {
	o := &clientOptions{}
	v := []grpc.DialOption{
		grpc.EmptyDialOption{},
	}
	WithOptions(v...)(o)
	if !reflect.DeepEqual(v, o.grpcOpts) {
		t.Errorf("expect %v but got %v", v, o.grpcOpts)
	}
}

func TestNewClient(t *testing.T) {
	conn, err := NewClient(
		context.Background(),
		WithDiscovery(&mockRegistry{}),
		WithTimeout(10*time.Second),
		WithEndpoint("abc"),
		WithMiddleware(EmptyMiddleware()),
		WithStreamMiddleware(EmptyMiddleware()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
}
