// Command service is the Forge server used by the cross-service end-to-end
// test. It runs in a container so that the test exercises real processes,
// real sockets, and real serialization rather than in-process shortcuts.
//
// It serves the same contract over gRPC and HTTP, and it can act as a client
// of another instance of itself, which is what makes a two-service test
// possible: the "edge" instance forwards to the "backend" instance and returns
// what it received.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/sylphylabs/forge"
	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/transport/grpc"
	"github.com/sylphylabs/forge/transport/http"
	_ "github.com/sylphylabs/forge/transport/http/transcoding"

	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
)

// ErrNotFound is the contract error the test matches against. It is declared
// here rather than generated so the fixture stays self-contained.
var ErrNotFound = forgeerrors.MustDefine(
	forgeerrors.KindNotFound, "e2e.v1", "GREETING_NOT_FOUND")

// ErrBackendFailed reports that the edge could not reach the backend.
var ErrBackendFailed = forgeerrors.MustDefine(
	forgeerrors.KindUnavailable, "e2e.v1", "BACKEND_FAILED")

// ErrInvalidRequest identifies the aggregate error. Violations cross the wire
// only under a declared identity (ADR-0012), so the fixture declares one and
// re-attaches it after aggregation, the way the validate middleware does.
var ErrInvalidRequest = forgeerrors.MustDefine(
	forgeerrors.KindInvalidArgument, "e2e.v1", "INVALID_REQUEST")

// nameError is the request name that asks the service to fail, so the test can
// check that a contract error keeps its identity across the wire.
const nameError = "error"

type server struct {
	pb.UnimplementedGreeterServer

	// backend, when set, is another instance this one forwards to.
	backend pb.GreeterClient
}

// SayHello answers directly, or forwards to the backend when this instance is
// the edge.
//
// The name selects the behavior under test:
//
//	"error"       return a contract error with a full identity
//	"aggregate"   return an aggregate error carrying violations
//	"internal"    return an error wrapping a secret cause
//	"forward:X"   ask the backend for X and return its answer verbatim
func (s *server) SayHello(ctx context.Context, req *pb.HelloRequest) (*pb.HelloReply, error) {
	name := req.GetName()

	if target, ok := strings.CutPrefix(name, "forward:"); ok {
		if s.backend == nil {
			return nil, ErrBackendFailed.Msg("this instance has no backend")
		}
		return s.backend.SayHello(ctx, &pb.HelloRequest{Name: target})
	}

	switch name {
	case nameError:
		return nil, ErrNotFound.
			Msgf("no greeting for %q", name).
			Meta("tenant", "acme").
			WithTraceID("trace-e2e")
	case "aggregate":
		var v forgeerrors.Violations
		v.Add("name", "must not be empty")
		v.Add("locale", "unsupported")
		return nil, forgeerrors.FromError(v.Err(forgeerrors.KindInvalidArgument)).
			WithDomain(ErrInvalidRequest.Domain()).
			WithReason(ErrInvalidRequest.Reason())
	case "internal":
		return nil, forgeerrors.MustDefine(forgeerrors.KindInternal, "e2e.v1", "LOOKUP_FAILED").
			Msg("lookup failed").
			Wrap(errors.New("dial tcp 10.0.0.1:5432: password=hunter2"))
	}
	return &pb.HelloReply{Message: "Hello " + name}, nil
}

// SayHelloStream echoes each request and ends with a contract error when asked,
// so the test can check that a stream's terminal error keeps its identity.
func (s *server) SayHelloStream(stream pb.Greeter_SayHelloStreamServer) error {
	for {
		req, err := stream.Recv()
		if err != nil {
			return nil
		}
		if req.GetName() == nameError {
			return ErrNotFound.Msg("stream ended with an error")
		}
		if err := stream.Send(&pb.HelloReply{Message: "Hello " + req.GetName()}); err != nil {
			return err
		}
	}
}

// registerStreamRoutes adds the SSE and WebSocket endpoints.
//
// The generated bindings cover unary calls; HTTP streaming is reached through
// the transport's own stream constructors, so the test drives them the way an
// application would.
func registerStreamRoutes(srv *http.Server) {
	route := srv.Route("/stream")

	// GET /stream/sse?name=X sends one message and then ends, either normally
	// or with a contract error when asked.
	route.GET("/sse", func(ctx http.Context) error {
		name := ctx.Request().URL.Query().Get("name")
		stream := http.NewServerSentEventServerStream(ctx)
		if err := stream.SendMsg(&pb.HelloReply{Message: "Hello " + name}); err != nil {
			return err
		}
		if name == nameError {
			return stream.Close(ErrNotFound.Msg("sse ended with an error").WithTraceID("trace-e2e"))
		}
		return stream.Close(nil)
	})

	// GET /stream/ws echoes each message, ending with a contract error when a
	// message asks for one.
	route.GET("/ws", func(ctx http.Context) error {
		stream, err := http.NewWebSocketServerStream(ctx)
		if err != nil {
			return err
		}
		for {
			var req pb.HelloRequest
			if err := stream.RecvMsg(&req); err != nil {
				return stream.Close(nil)
			}
			if req.GetName() == nameError {
				return stream.Close(ErrNotFound.Msg("websocket ended with an error").WithTraceID("trace-e2e"))
			}
			if err := stream.SendMsg(&pb.HelloReply{Message: "Hello " + req.GetName()}); err != nil {
				return stream.Close(nil)
			}
		}
	})
}

func main() {
	if err := run(); err != nil {
		slog.Error("service failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	srv := &server{}

	// The edge instance is told where its backend lives. Reaching it over gRPC
	// exercises the client interceptor that restores Forge errors.
	if addr := os.Getenv("BACKEND_ADDR"); addr != "" {
		conn, err := grpc.NewClient(context.Background(), grpc.WithTarget(addr))
		if err != nil {
			return fmt.Errorf("dial backend: %w", err)
		}
		defer func() { _ = conn.Close() }()
		srv.backend = pb.NewGreeterClient(conn)
	}

	grpcSrv := grpc.NewServer(grpc.WithAddress(":9000"))
	pb.RegisterGreeterServer(grpcSrv, srv)

	httpSrv := http.NewServer(http.WithAddress(":8000"))
	pb.RegisterGreeterHTTPServer(httpSrv, srv)
	registerStreamRoutes(httpSrv)

	app := forge.New(
		forge.WithName("e2e-service"),
		forge.WithServer(grpcSrv, httpSrv),
	)
	return app.Run()
}
