package grpc_test

// The examples in this file mirror the snippets in docs/agent/middleware.md
// so that the guide cannot drift from the API without breaking the build.
// When one of these stops compiling, fix the guide together with the example.

import (
	"context"
	"fmt"
	"log/slog"

	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/logging"
	"github.com/sylphylabs/forge/middleware/recovery"
	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
	"google.golang.org/grpc"
)

type greeterServer struct {
	pb.UnimplementedGreeterServer
}

func (greeterServer) SayHello(_ context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	return &pb.HelloReply{Message: "hello " + req.GetName()}, nil
}

// requireScope and throttleStream stand in for the guide's method-scoped
// middleware; any UnaryMiddleware / StreamMiddleware fits the plan the same
// way.
func requireScope(string) middleware.UnaryMiddleware {
	return func(next middleware.UnaryHandler) middleware.UnaryHandler { return next }
}

func throttleStream() middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler { return next }
}

func streamAuth() middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler { return next }
}

// Example_serverWideMiddleware mirrors "Attaching server middleware »
// Server-wide": gRPC has both WithMiddleware for unary methods and
// WithStreamMiddleware for streaming methods, composed once inside NewServer.
func Example_serverWideMiddleware() {
	logger := slog.Default()

	grpcSrv := forgegrpc.NewServer(
		forgegrpc.WithAddress("127.0.0.1:0"),
		forgegrpc.WithMiddleware(recovery.Recovery(), logging.Server(logger)),
		forgegrpc.WithStreamMiddleware(streamAuth()),
	)

	_ = grpcSrv
	fmt.Println("constructed")
	// Output: constructed
}

// Example_generatedPlan mirrors "Per-service (generated)": build the wrapped
// service with the plan, check the error, then register it.
func Example_generatedPlan() {
	logger := slog.Default()

	plan := pb.GreeterMiddleware{
		Unary: []middleware.UnaryMiddleware{
			recovery.Recovery(),
			logging.Server(logger),
		},
		Stream: []middleware.StreamMiddleware{
			recovery.Stream(),
			streamAuth(),
		},
		Methods: pb.GreeterMethodMiddleware{
			SayHello:       []middleware.UnaryMiddleware{requireScope("greet")},
			SayHelloStream: []middleware.StreamMiddleware{throttleStream()},
		},
	}

	grpcSrv := forgegrpc.NewServer(forgegrpc.WithAddress("127.0.0.1:0"))
	service, err := pb.WrapGreeterGRPCServer(greeterServer{}, plan)
	if err != nil {
		fmt.Println(err)
		return
	}
	pb.RegisterGreeterServer(grpcSrv, service)

	fmt.Println("registered")
	// Output: registered
}

// Example_client mirrors "Client middleware": client middleware is a client
// option, WithClientMiddleware.
func Example_client() {
	conn, err := forgegrpc.NewClient(context.Background(),
		forgegrpc.WithTarget("dns:///example.invalid:9000"),
		forgegrpc.WithClientMiddleware(logging.Client(slog.Default())),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer func(conn *grpc.ClientConn) { _ = conn.Close() }(conn)

	fmt.Println("constructed")
	// Output: constructed
}
