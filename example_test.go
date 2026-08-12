package forge_test

// The examples in this file mirror the snippets in docs/agent/application.md
// so that the guide cannot drift from the API without breaking the build.
// When one of these stops compiling, fix the guide together with the example.

import (
	"context"
	"fmt"
	"time"

	"github.com/sylphylabs/forge"
	"github.com/sylphylabs/forge/registry"
	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

// noopRegistrar stands in for a registry integration (contrib/registry/...).
type noopRegistrar struct{}

func (noopRegistrar) Register(context.Context, *registry.ServiceInstance) error   { return nil }
func (noopRegistrar) Deregister(context.Context, *registry.ServiceInstance) error { return nil }

// Example_minimalService mirrors "Minimal service". It has no Output comment,
// so `go test` compiles it without running it: the guide's version blocks in
// Run until a stop signal arrives.
func Example_minimalService() {
	httpSrv := forgehttp.NewServer(forgehttp.WithAddress(":8000"))
	grpcSrv := forgegrpc.NewServer(forgegrpc.WithAddress(":9000"))

	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithVersion("v1.0.0"),
		forge.WithServer(httpSrv, grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}

// pool stands in for the guide's application-owned resource whose lifecycle
// the hooks manage.
type pool struct{}

func (pool) Ping(context.Context) error { return nil }
func (pool) Close() error               { return nil }

// Example_lifecycle mirrors "Lifecycle": hooks and the three independent
// timeouts are all construction options.
func Example_lifecycle() {
	httpSrv := forgehttp.NewServer(forgehttp.WithAddress("127.0.0.1:0"))
	var reg noopRegistrar
	var pool pool

	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithServer(httpSrv),
		forge.WithRegistrar(reg),
		forge.WithRegistrarTimeout(5*time.Second),
		forge.WithStopTimeout(15*time.Second),
		forge.WithAfterStopTimeout(5*time.Second),
		forge.WithBeforeStart(func(ctx context.Context) error { return pool.Ping(ctx) }),
		forge.WithAfterStop(func(ctx context.Context) error { return pool.Close() }),
	)

	_ = app
	fmt.Println("constructed")
	// Output: constructed
}

// Example_fromContext mirrors the guide's identity snippet: inside a handler
// or hook, recover application identity from the context.
func Example_fromContext() {
	app := forge.New(forge.WithName("helloworld"))
	ctx := forge.NewContext(context.Background(), app)

	if info, ok := forge.FromContext(ctx); ok {
		fmt.Println(info.Name())
		_ = info.Endpoints()
	}
	// Output: helloworld
}

// closingSuite mirrors the guide's tracing suite: an integration bundles its
// options — here an AfterStop hook that shuts the integration down — so an
// application adopts them in one WithSuite call. The guide's version closes
// an OpenTelemetry TracerProvider; the shape is the same for any resource.
type closingSuite struct{ closer interface{ Close() error } }

func (s closingSuite) Options() []forge.Option {
	return []forge.Option{
		forge.WithAfterStop(func(ctx context.Context) error {
			return s.closer.Close()
		}),
	}
}

func Example_suite() {
	app := forge.New(
		forge.WithName("helloworld"),
		forge.WithSuite(closingSuite{closer: pool{}}),
	)

	_ = app
	fmt.Println("constructed")
	// Output: constructed
}
