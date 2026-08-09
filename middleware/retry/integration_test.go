package retry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sylphylabs/forge/errors"
	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
	"github.com/sylphylabs/forge/middleware/retry"
	transportgrpc "github.com/sylphylabs/forge/transport/grpc"
	transporthttp "github.com/sylphylabs/forge/transport/http"

	// register the json codec used by the HTTP client's default encoder.
	_ "github.com/sylphylabs/forge/encoding/json"
)

// flakyGreeter fails with a retryable error until failures attempts have
// been consumed, then succeeds.
type flakyGreeter struct {
	pb.UnimplementedGreeterServer
	calls    atomic.Int64
	failures int64
}

func (s *flakyGreeter) SayHello(_ context.Context, in *pb.HelloRequest) (*pb.HelloReply, error) {
	if s.calls.Add(1) <= s.failures {
		return nil, errors.ServiceUnavailable("FLAKY", "transient failure")
	}
	return &pb.HelloReply{Message: fmt.Sprintf("Hello %s", in.Name)}, nil
}

// fastPolicy keeps integration tests quick without an injected sleeper.
var fastPolicy = retry.Policy{Attempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: 2 * time.Millisecond}

func TestGRPCClientRetries(t *testing.T) {
	ctx := context.Background()
	greeter := &flakyGreeter{failures: 2}
	srv := transportgrpc.NewServer(transportgrpc.Address("127.0.0.1:0"))
	pb.RegisterGreeterServer(srv, greeter)
	endpoint, err := srv.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = srv.Start(ctx)
	}()
	t.Cleanup(func() { _ = srv.Stop(ctx) })

	m, err := retry.Client(retry.WithPolicy(fastPolicy))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := transportgrpc.NewClient(ctx,
		transportgrpc.WithEndpoint(endpoint.Host),
		transportgrpc.WithMiddleware(m),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	reply, err := pb.NewGreeterClient(conn).SayHello(ctx, &pb.HelloRequest{Name: "forge"})
	if err != nil {
		t.Fatalf("call must succeed within the retry budget, got %v", err)
	}
	if reply.Message != "Hello forge" {
		t.Fatalf("unexpected reply %q", reply.Message)
	}
	if got := greeter.calls.Load(); got != 3 {
		t.Fatalf("server must observe 3 attempts, got %d", got)
	}

	// A fresh flaky streak deeper than the budget exhausts it and
	// surfaces the last error.
	greeter.calls.Store(0)
	greeter.failures = 99
	if _, err := pb.NewGreeterClient(conn).SayHello(ctx, &pb.HelloRequest{Name: "forge"}); !errors.IsServiceUnavailable(err) {
		t.Fatalf("want the last transient error, got %v", err)
	}
	if got := greeter.calls.Load(); got != 3 {
		t.Fatalf("server must observe exactly the retry budget, got %d", got)
	}
}

func TestHTTPClientRetries(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	var bodies [][]byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, body)
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	t.Cleanup(ts.Close)

	m, err := retry.Client(retry.WithPolicy(fastPolicy))
	if err != nil {
		t.Fatal(err)
	}
	client, err := transporthttp.NewClient(ctx,
		transporthttp.WithEndpoint(ts.Listener.Addr().String()),
		transporthttp.WithMiddleware(m),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var reply struct {
		Message string `json:"message"`
	}
	args := map[string]string{"name": "forge"}
	if err := client.Invoke(ctx, http.MethodPost, "/hello", args, &reply); err != nil {
		t.Fatalf("call must succeed within the retry budget, got %v", err)
	}
	if reply.Message != "ok" {
		t.Fatalf("unexpected reply %q", reply.Message)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("server must observe 3 attempts, got %d", got)
	}
	// Every attempt must carry the full request body: the middleware
	// rewinds it between attempts.
	want, _ := json.Marshal(args)
	for i, b := range bodies {
		if string(b) != string(want) {
			t.Fatalf("attempt %d body: want %s, got %s", i+1, want, b)
		}
	}
}

func TestHTTPClientDoesNotRetryClientErrors(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(ts.Close)

	m, err := retry.Client(retry.WithPolicy(fastPolicy))
	if err != nil {
		t.Fatal(err)
	}
	client, err := transporthttp.NewClient(ctx,
		transporthttp.WithEndpoint(ts.Listener.Addr().String()),
		transporthttp.WithMiddleware(m),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var reply struct{}
	if err := client.Invoke(ctx, http.MethodGet, "/hello", nil, &reply); !errors.IsBadRequest(err) {
		t.Fatalf("want the client error unchanged, got %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("a deterministic client error must not be retried, got %d attempts", got)
	}
}
