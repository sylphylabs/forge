// Package e2e tests Forge against itself across a real network.
//
// Every other test in this repository runs in one process, which means a
// mistake in how a test reaches the code under test can look like a passing
// contract. That happened during development: a probe dialed a raw gRPC
// client instead of Forge's, saw an error arrive without its identity, and was
// reported as a transport defect. It was not — the probe had bypassed the
// interceptor that restores identity.
//
// These tests remove that class of mistake by using only the public entry
// points against services running in containers.
//
// transport/message is not covered here, for two reasons. Message delivery is
// one-way: a handler error has no caller to return to, so it becomes an ack or
// a nack to the broker rather than a value crossing a process boundary. There
// is no error contract to verify, which is also why that package uses plain
// stdlib errors rather than Forge ones. And a broker driver is a dependency of
// its own contrib module, not of this one, so importing an adapter here would
// put it in the root module's dependency graph.
//
// The equivalent tests for the message transport live with the adapter that
// owns the broker dependency: see contrib/message/rabbitmq/e2e_message_test.go,
// which runs a subscribe annotation through a real broker to a handler.
package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/transport/grpc"
	forgehttp "github.com/sylphylabs/forge/transport/http"
	_ "github.com/sylphylabs/forge/transport/http/transcoding"

	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
)

// The identities the fixture service declares. A test asserts against these to
// prove that a sentinel still matches after crossing two process boundaries.
var (
	errNotFound      = forgeerrors.MustDefine(forgeerrors.KindNotFound, "e2e.v1", "GREETING_NOT_FOUND")
	errLookupFailed  = forgeerrors.MustDefine(forgeerrors.KindInternal, "e2e.v1", "LOOKUP_FAILED")
	edgeGRPCEndpoint = "127.0.0.1:19000"
	edgeHTTPEndpoint = "127.0.0.1:18000"
)

// The stack is expensive to build, so every test shares one.
func TestMain(m *testing.M) {
	if os.Getenv("FORGE_E2E") == "" {
		// Skipping is stated by each test rather than here, so a reader sees
		// why nothing ran.
		os.Exit(m.Run())
	}
	if err := compose("up", "--build", "--detach", "--wait"); err != nil {
		panic("compose up: " + err.Error())
	}
	code := m.Run()
	_ = compose("down", "--volumes")
	os.Exit(code)
}

func compose(args ...string) error {
	cmd := exec.Command("docker", append([]string{"compose", "-f", "compose.yaml"}, args...)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// requireStack skips unless the containers are running.
//
// The stack is opt-in because it needs Docker and takes time to build. Naming
// the variable in the skip message tells a reader how to run these.
//
// Run these on their own — `go test ./internal/e2e` — rather than inside a
// repository-wide `-race` sweep. Two containers, a Go toolchain building them,
// and a race-instrumented test binary compete for the same machine, and the
// combination has exhausted memory on a developer laptop.
func requireStack(t *testing.T) {
	t.Helper()
	if os.Getenv("FORGE_E2E") == "" {
		t.Skip("set FORGE_E2E=1 to run the cross-service tests (needs Docker)")
	}
}

func grpcClient(t *testing.T) pb.GreeterClient {
	t.Helper()
	conn, err := grpc.NewClient(t.Context(), grpc.WithEndpoint(edgeGRPCEndpoint))
	if err != nil {
		t.Fatalf("dial edge: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewGreeterClient(conn)
}

func TestGRPCUnarySucceeds(t *testing.T) {
	requireStack(t)
	reply, err := grpcClient(t).SayHello(t.Context(), &pb.HelloRequest{Name: "forge"})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if reply.GetMessage() != "Hello forge" {
		t.Errorf("message = %q", reply.GetMessage())
	}
}

// A contract error must arrive as the same sentinel the callee declared, with
// its metadata and trace intact.
func TestGRPCErrorKeepsIdentity(t *testing.T) {
	requireStack(t)
	_, err := grpcClient(t).SayHello(t.Context(), &pb.HelloRequest{Name: "error"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !forgeerrors.Is(err, errNotFound) {
		t.Errorf("error does not match its sentinel: %v", err)
	}
	got := forgeerrors.FromError(err)
	if got.Kind() != forgeerrors.KindNotFound {
		t.Errorf("kind = %v", got.Kind())
	}
	if got.Metadata()["tenant"] != "acme" {
		t.Errorf("metadata = %v", got.Metadata())
	}
	if got.TraceID() != "trace-e2e" {
		t.Errorf("trace ID = %q", got.TraceID())
	}
}

// Two hops: the client calls the edge, the edge calls the backend over gRPC,
// and the backend's error must still be matchable at the far end.
func TestErrorSurvivesTwoHops(t *testing.T) {
	requireStack(t)
	_, err := grpcClient(t).SayHello(t.Context(), &pb.HelloRequest{Name: "forward:error"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !forgeerrors.Is(err, errNotFound) {
		t.Errorf("identity lost after two hops: %v", err)
	}
	if got := forgeerrors.FromError(err).TraceID(); got != "trace-e2e" {
		t.Errorf("trace ID lost after two hops: %q", got)
	}
}

// A cause is local by construction, so no hop may disclose it.
func TestCauseNeverCrossesTheNetwork(t *testing.T) {
	requireStack(t)
	for _, name := range []string{"internal", "forward:internal"} {
		_, err := grpcClient(t).SayHello(t.Context(), &pb.HelloRequest{Name: name})
		if err == nil {
			t.Fatalf("%s: expected an error", name)
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "5432") {
			t.Errorf("%s: the cause reached the caller: %v", name, err)
		}
		if !forgeerrors.Is(err, errLookupFailed) {
			t.Errorf("%s: identity lost: %v", name, err)
		}
	}
}

// Every violation must reach the caller so a client can show a user each bad
// field.
func TestAggregateErrorSurvives(t *testing.T) {
	requireStack(t)
	_, err := grpcClient(t).SayHello(t.Context(), &pb.HelloRequest{Name: "aggregate"})
	if err == nil {
		t.Fatal("expected an error")
	}
	violations := forgeerrors.FromError(err).Violations()
	if len(violations) != 2 {
		t.Fatalf("violations = %d, want 2: %v", len(violations), err)
	}
	if violations[0].Field != "name" || violations[1].Field != "locale" {
		t.Errorf("violations lost order or content: %+v", violations)
	}
}

func TestGRPCStreamEchoesAndFailsWithIdentity(t *testing.T) {
	requireStack(t)
	stream, err := grpcClient(t).SayHelloStream(t.Context())
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if err := stream.Send(&pb.HelloRequest{Name: "forge"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	reply, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv: %v", err)
	}
	if reply.GetMessage() != "Hello forge" {
		t.Errorf("message = %q", reply.GetMessage())
	}

	if err := stream.Send(&pb.HelloRequest{Name: "error"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, err := stream.Recv(); !forgeerrors.Is(err, errNotFound) {
		t.Errorf("stream terminal error lost its identity: %v", err)
	}
}

// The same service over HTTP must produce the same identity, so a caller is not
// forced to learn two error contracts.
func TestHTTPErrorMatchesGRPC(t *testing.T) {
	requireStack(t)
	client, err := forgehttp.NewClient(t.Context(), forgehttp.WithEndpoint(edgeHTTPEndpoint))
	if err != nil {
		t.Fatalf("dial edge: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var reply pb.HelloReply
	callErr := client.Invoke(t.Context(), http.MethodGet, "/helloworld/error", nil, &reply,
		forgehttp.Operation("/helloworld.Greeter/SayHello"))
	if callErr == nil {
		t.Fatal("expected an error")
	}
	if !forgeerrors.Is(callErr, errNotFound) {
		t.Errorf("HTTP error does not match the same sentinel as gRPC: %v", callErr)
	}
}

// The error body is one document whatever the request asked for.
func TestHTTPErrorBodyIsProblemJSON(t *testing.T) {
	requireStack(t)
	for _, accept := range []string{"application/json", "application/protojson", "*/*"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
			"http://"+edgeHTTPEndpoint+"/helloworld/error", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", accept)

		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("Accept %q: %v", accept, err)
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()

		if got := res.Header.Get("Content-Type"); !strings.HasPrefix(got, forgehttp.ProblemContentType) {
			t.Errorf("Accept %q: content type = %q, want %q", accept, got, forgehttp.ProblemContentType)
		}
		var doc struct {
			Kind   string `json:"kind"`
			Domain string `json:"domain"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("Accept %q: body is not JSON: %s", accept, body)
		}
		if doc.Kind != "NOT_FOUND" || doc.Domain != "e2e.v1" || doc.Reason != "GREETING_NOT_FOUND" {
			t.Errorf("Accept %q: body = %s", accept, body)
		}
	}
}

// SSE carries a terminal error as the same Problem document a unary response
// uses, so a stream failure is matchable against the same sentinel.
func TestSSEStreamCarriesIdentity(t *testing.T) {
	requireStack(t)
	client, err := forgehttp.NewClient(t.Context(), forgehttp.WithEndpoint(edgeHTTPEndpoint))
	if err != nil {
		t.Fatalf("dial edge: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	stream, err := client.ServerSentEvent(t.Context(), http.MethodGet, "/stream/sse?name=error", nil)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	var reply pb.HelloReply
	if err := stream.RecvMsg(&reply); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if reply.GetMessage() != "Hello error" {
		t.Errorf("message = %q", reply.GetMessage())
	}

	err = stream.RecvMsg(&reply)
	if !forgeerrors.Is(err, errNotFound) {
		t.Fatalf("terminal error does not match its sentinel: %v", err)
	}
	if got := forgeerrors.FromError(err).TraceID(); got != "trace-e2e" {
		t.Errorf("trace ID = %q, want trace-e2e", got)
	}
}

// A stream that ends normally reports io.EOF, not an error identity.
func TestSSEStreamEndsCleanly(t *testing.T) {
	requireStack(t)
	client, err := forgehttp.NewClient(t.Context(), forgehttp.WithEndpoint(edgeHTTPEndpoint))
	if err != nil {
		t.Fatalf("dial edge: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	stream, err := client.ServerSentEvent(t.Context(), http.MethodGet, "/stream/sse?name=forge", nil)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	var reply pb.HelloReply
	if err := stream.RecvMsg(&reply); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if reply.GetMessage() != "Hello forge" {
		t.Errorf("message = %q", reply.GetMessage())
	}
	if err := stream.RecvMsg(&reply); !errors.Is(err, io.EOF) {
		t.Errorf("clean end = %v, want io.EOF", err)
	}
}

// WebSocket is bidirectional, so it exercises both directions of the same
// stream plus the terminal error.
func TestWebSocketStreamEchoesAndCarriesIdentity(t *testing.T) {
	requireStack(t)
	client, err := forgehttp.NewClient(t.Context(), forgehttp.WithEndpoint(edgeHTTPEndpoint))
	if err != nil {
		t.Fatalf("dial edge: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	stream, err := client.WebSocket(t.Context(), "/stream/ws")
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}

	if err := stream.SendMsg(&pb.HelloRequest{Name: "forge"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	var reply pb.HelloReply
	if err := stream.RecvMsg(&reply); err != nil {
		t.Fatalf("recv: %v", err)
	}
	if reply.GetMessage() != "Hello forge" {
		t.Errorf("message = %q", reply.GetMessage())
	}

	if err := stream.SendMsg(&pb.HelloRequest{Name: "error"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	err = stream.RecvMsg(&reply)
	if !forgeerrors.Is(err, errNotFound) {
		t.Fatalf("terminal error does not match its sentinel: %v", err)
	}
	if got := forgeerrors.FromError(err).TraceID(); got != "trace-e2e" {
		t.Errorf("trace ID = %q, want trace-e2e", got)
	}
}

// Concurrency across a real network, which is where a shared sentinel is most
// exposed: many goroutines derive from the same package-level value while the
// transport decodes into fresh ones. A single mutated sentinel would show up
// here as an identity that stops matching, or as metadata from another call.
func TestConcurrentErrorsKeepTheirIdentity(t *testing.T) {
	requireStack(t)
	client := grpcClient(t)

	const goroutines, perGoroutine = 16, 25
	errs := make(chan error, goroutines*perGoroutine)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perGoroutine {
				_, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "error"})
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if !forgeerrors.Is(err, errNotFound) {
			t.Fatalf("identity lost under concurrency: %v", err)
		}
		got := forgeerrors.FromError(err)
		if got.Metadata()["tenant"] != "acme" || got.TraceID() != "trace-e2e" {
			t.Fatalf("public data crossed between calls: %+v", got)
		}
	}

	// The sentinel the assertions matched against must be untouched.
	if errNotFound.Message() != "" || errNotFound.Metadata() != nil {
		t.Errorf("the sentinel was mutated: msg=%q meta=%v",
			errNotFound.Message(), errNotFound.Metadata())
	}
}

// Mixed kinds in flight together: a decoder that reused state between calls
// would hand one call's violations or trace to another.
func TestConcurrentMixedErrorsDoNotBleed(t *testing.T) {
	requireStack(t)
	client := grpcClient(t)

	var wg sync.WaitGroup
	fail := make(chan string, 64)

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "aggregate"})
				got := forgeerrors.FromError(err)
				if n := len(got.Violations()); n != 2 {
					fail <- fmt.Sprintf("aggregate: violations = %d, want 2", n)
					return
				}
				if got.TraceID() != "" {
					fail <- fmt.Sprintf("aggregate: picked up a trace ID: %q", got.TraceID())
					return
				}
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, err := client.SayHello(t.Context(), &pb.HelloRequest{Name: "error"})
				got := forgeerrors.FromError(err)
				if len(got.Violations()) != 0 {
					fail <- "not-found: picked up violations from another call"
					return
				}
				if got.TraceID() != "trace-e2e" {
					fail <- fmt.Sprintf("not-found: trace ID = %q", got.TraceID())
					return
				}
			}
		}()
	}
	wg.Wait()
	close(fail)

	for msg := range fail {
		t.Error(msg)
	}
}
