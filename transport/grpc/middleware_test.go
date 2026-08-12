package grpc

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
	"github.com/sylphylabs/forge/middleware"
)

// startTestServer starts a Server with the given options and returns a
// connected Greeter client.
func startTestServer(t *testing.T, srv *Server) pb.GreeterClient {
	t.Helper()
	pb.RegisterGreeterServer(srv, &server{})
	go func() { _ = srv.Start(context.Background()) }()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	waitServing(t, srv)
	u, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := NewClient(context.Background(), WithTarget(u.Host))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewGreeterClient(conn)
}

func waitServing(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !srv.Healthz() {
		if time.Now().After(deadline) {
			t.Fatal("server never became healthy")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The backstop must fire with zero middleware configured: it is the
// transport's guarantee, not an optional layer.
func TestBackstopRecoversUnaryPanic(t *testing.T) {
	client := startTestServer(t, NewServer())

	_, err := client.SayHello(context.Background(), &pb.HelloRequest{Name: "panic"})
	if err == nil {
		t.Fatal("a panicking unary handler must surface an error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v carries no gRPC status", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want %v", st.Code(), codes.Internal)
	}
	if strings.Contains(st.Message(), "server panic") {
		t.Errorf("status message %q discloses the panic value", st.Message())
	}

	// The process survived: the same server answers the next call.
	reply, err := client.SayHello(context.Background(), &pb.HelloRequest{Name: "forge"})
	if err != nil || reply.GetMessage() != "Hello forge" {
		t.Fatalf("call after panic = %v, %v", reply, err)
	}
}

func TestBackstopRecoversStreamPanic(t *testing.T) {
	client := startTestServer(t, NewServer())

	stream, err := client.SayHelloStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.HelloRequest{Name: "panic"}); err != nil {
		t.Fatal(err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("a panicking stream handler must surface an error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("error %v carries no gRPC status", err)
	}
	if st.Code() != codes.Internal {
		t.Errorf("code = %v, want %v", st.Code(), codes.Internal)
	}
	if strings.Contains(st.Message(), "server panic") {
		t.Errorf("status message %q discloses the panic value", st.Message())
	}

	// The process survived: the same server serves the next stream.
	stream, err = client.SayHelloStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.HelloRequest{Name: "cc"}); err != nil {
		t.Fatal(err)
	}
	if reply, err := stream.Recv(); err != nil || reply.GetMessage() != "hello cc" {
		t.Fatalf("stream after panic = %v, %v", reply, err)
	}
}

type orderRecorder struct {
	mu    sync.Mutex
	order []string
}

func (r *orderRecorder) add(name string) {
	r.mu.Lock()
	r.order = append(r.order, name)
	r.mu.Unlock()
}

func (r *orderRecorder) unary(name string) middleware.UnaryMiddleware {
	return func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			r.add(name)
			return next(ctx, req)
		}
	}
}

func (r *orderRecorder) stream(name string) middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			r.add(name)
			return next(request, stream)
		}
	}
}

func (r *orderRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// Server-wide middleware runs outside (before) the generated per-service
// plan, on both the unary and the stream path.
func TestServerMiddlewareRunsOutsideGeneratedPlan(t *testing.T) {
	var rec orderRecorder
	srv := NewServer(
		WithMiddleware(rec.unary("server-wide")),
		WithStreamMiddleware(rec.stream("server-wide-stream")),
	)
	service, err := pb.WrapGreeterGRPCServer(&server{}, pb.GreeterMiddleware{
		Unary:  []middleware.UnaryMiddleware{rec.unary("generated")},
		Stream: []middleware.StreamMiddleware{rec.stream("generated-stream")},
	})
	if err != nil {
		t.Fatal(err)
	}
	pb.RegisterGreeterServer(srv, service)
	go func() { _ = srv.Start(context.Background()) }()
	t.Cleanup(func() { _ = srv.Stop(context.Background()) })
	waitServing(t, srv)
	u, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	// Health checking opens a Watch stream that would also run the
	// server-wide stream middleware; disable it so the recorder sees only
	// the Greeter calls.
	conn, err := NewClient(context.Background(), WithTarget(u.Host), WithHealthCheck(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client := pb.NewGreeterClient(conn)

	if _, err := client.SayHello(context.Background(), &pb.HelloRequest{Name: "forge"}); err != nil {
		t.Fatal(err)
	}
	if got := rec.snapshot(); len(got) != 2 || got[0] != "server-wide" || got[1] != "generated" {
		t.Fatalf("unary order = %v, want [server-wide generated]", got)
	}

	rec.mu.Lock()
	rec.order = nil
	rec.mu.Unlock()

	stream, err := client.SayHelloStream(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&pb.HelloRequest{Name: "cc"}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := rec.snapshot(); len(got) == 2 {
			if got[0] != "server-wide-stream" || got[1] != "generated-stream" {
				t.Fatalf("stream order = %v, want [server-wide-stream generated-stream]", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream middleware never both ran: %v", rec.snapshot())
		}
		time.Sleep(time.Millisecond)
	}
}

// A nil middleware fails composition inside NewServer; Start and Endpoint
// report it, the way they report a bad listener.
func TestNewServerReportsBadMiddleware(t *testing.T) {
	srv := NewServer(WithMiddleware(nil))
	if err := srv.Start(context.Background()); err == nil {
		t.Error("Start with a nil unary middleware must fail")
	}
	if _, err := srv.Endpoint(); err == nil {
		t.Error("Endpoint with a nil unary middleware must fail")
	}
	srv = NewServer(WithStreamMiddleware(nil))
	if err := srv.Start(context.Background()); err == nil {
		t.Error("Start with a nil stream middleware must fail")
	}
}
