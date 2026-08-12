package http_test

// The examples in this file mirror the server and client snippets in
// docs/agent/middleware.md and docs/agent/application.md so that the guides
// cannot drift from the API without breaking the build. When one of these
// stops compiling, fix the guide together with the example.

import (
	"context"
	"fmt"
	"log/slog"

	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/logging"
	"github.com/sylphylabs/forge/middleware/recovery"
	forgehttp "github.com/sylphylabs/forge/transport/http"
)

type greeterServer struct{}

func (greeterServer) SayHello(_ context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{Message: "hello " + req.GetName()}, nil
}

// requireScope stands in for the guide's method-scoped middleware; any
// UnaryMiddleware fits the plan the same way.
func requireScope(string) middleware.UnaryMiddleware {
	return func(next middleware.UnaryHandler) middleware.UnaryHandler { return next }
}

// Example_serverWideMiddleware mirrors "Attaching server middleware »
// Server-wide": middleware is a construction option, composed exactly once
// inside NewServer.
func Example_serverWideMiddleware() {
	logger := slog.Default()

	httpSrv := forgehttp.NewServer(
		forgehttp.WithAddress("127.0.0.1:0"),
		forgehttp.WithMiddleware(recovery.Recovery(), logging.Server(logger)),
	)

	_ = httpSrv
	fmt.Println("constructed")
	// Output: constructed
}

// Example_generatedPlan mirrors "Per-service (generated)" for HTTP: build
// the wrapped service with the plan, check the error, then register it.
func Example_generatedPlan() {
	logger := slog.Default()

	srv := forgehttp.NewServer(forgehttp.WithAddress("127.0.0.1:0"))
	service, err := pb.WrapGreeterHTTPServer(greeterServer{}, pb.GreeterMiddleware{
		Unary: []middleware.UnaryMiddleware{
			recovery.Recovery(),
			logging.Server(logger),
		},
		Methods: pb.GreeterMethodMiddleware{
			SayHello: []middleware.UnaryMiddleware{requireScope("greet")},
		},
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	pb.RegisterGreeterHTTPServer(srv, service)

	fmt.Println("registered")
	// Output: registered
}

// Example_client mirrors "Client middleware": client middleware is a client
// option, WithClientMiddleware.
func Example_client() {
	endpoint := "http://127.0.0.1:8000"

	client, err := forgehttp.NewClient(context.Background(),
		forgehttp.WithTarget(endpoint),
		forgehttp.WithClientMiddleware(logging.Client(slog.Default())),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	fmt.Println("constructed")
	// Output: constructed
}
