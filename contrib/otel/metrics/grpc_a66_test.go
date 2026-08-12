package metrics_test

import (
	"context"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstats "google.golang.org/grpc/stats"
	grpcotel "google.golang.org/grpc/stats/opentelemetry"
	"google.golang.org/grpc/status"

	forgetracing "github.com/sylphylabs/forge/contrib/otel/tracing"
	pb "github.com/sylphylabs/forge/internal/testdata/helloworld"
	forgegrpc "github.com/sylphylabs/forge/transport/grpc"
)

const a66RetryServiceConfig = `{
	"methodConfig": [{
		"name": [{"service": "helloworld.Greeter", "method": "SayHello"}],
		"retryPolicy": {
			"maxAttempts": 3,
			"initialBackoff": "0.001s",
			"maxBackoff": "0.001s",
			"backoffMultiplier": 1.0,
			"retryableStatusCodes": ["UNAVAILABLE"]
		}
	}]
}`

const (
	a66ClientStreamingFullMethodName = "/sylphy.metrics.v1.StreamService/ClientStreaming"
	a66ServerStreamingFullMethodName = "/sylphy.metrics.v1.StreamService/ServerStreaming"
	a66FullDuplexFullMethodName      = "/sylphy.metrics.v1.StreamService/FullDuplex"
	a66HalfDuplexFullMethodName      = "/sylphy.metrics.v1.StreamService/HalfDuplex"
)

const (
	a66ClientStreamingStreamIndex = iota
	a66ServerStreamingStreamIndex
	a66FullDuplexStreamIndex
	a66HalfDuplexStreamIndex
)

var a66DurationMetricNames = []string{
	grpcotel.ClientCallDurationMetricName,
	grpcotel.ClientAttemptDurationMetricName,
	grpcotel.ServerCallDurationMetricName,
}

func Example_a66DurationOnly() {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	metricSet := grpcstats.NewMetricSet(
		grpcotel.ClientCallDurationMetricName,
		grpcotel.ClientAttemptDurationMetricName,
		grpcotel.ServerCallDurationMetricName,
	)
	otelOptions := grpcotel.Options{
		MetricsOptions: grpcotel.MetricsOptions{
			MeterProvider: provider,
			Metrics:       metricSet,
		},
		// Leave TraceOptions unset so existing tracing remains the sole span owner.
	}

	_ = forgegrpc.NewServer(
		forgegrpc.WithOptions(grpcotel.ServerOption(otelOptions)),
	)
	conn, err := forgegrpc.NewClient(
		context.Background(),
		forgegrpc.WithTarget("dns:///example.invalid:443"),
		forgegrpc.WithDialOptions(grpcotel.DialOption(otelOptions)),
	)
	if err != nil {
		return
	}
	defer conn.Close()
}

func TestGRPCA66DurationOnly(t *testing.T) {
	fixture := newA66Fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	t.Run("unary OK and error", func(t *testing.T) {
		if _, err := fixture.client.SayHello(ctx, &pb.HelloRequest{Name: "ok"}); err != nil {
			t.Fatalf("SayHello(ok) failed: %v", err)
		}
		if _, err := fixture.client.SayHello(ctx, &pb.HelloRequest{Name: "error"}); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("SayHello(error) code = %v, want %v (err = %v)", status.Code(err), codes.InvalidArgument, err)
		}

		method := "helloworld.Greeter/SayHello"
		waitForA66Count(t, fixture.reader, grpcotel.ClientCallDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "OK",
		}, 1)
		waitForA66Count(t, fixture.reader, grpcotel.ClientCallDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "INVALID_ARGUMENT",
		}, 1)
		waitForA66Count(t, fixture.reader, grpcotel.ClientAttemptDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "INVALID_ARGUMENT",
		}, 1)
		waitForA66Count(t, fixture.reader, grpcotel.ServerCallDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "INVALID_ARGUMENT",
		}, 1)
	})

	streamTests := []struct {
		name       string
		fullMethod string
		start      func(context.Context, *testing.T, *a66Fixture) func(*testing.T)
	}{
		{
			name:       "client streaming",
			fullMethod: a66ClientStreamingFullMethodName,
			start:      startA66ClientStreamingCall,
		},
		{
			name:       "server streaming",
			fullMethod: a66ServerStreamingFullMethodName,
			start:      startA66ServerStreamingCall,
		},
		{
			name:       "bidirectional full duplex",
			fullMethod: a66FullDuplexFullMethodName,
			start:      startA66FullDuplexCall,
		},
		{
			name:       "bidirectional half duplex",
			fullMethod: a66HalfDuplexFullMethodName,
			start:      startA66HalfDuplexCall,
		},
	}
	for _, streamTest := range streamTests {
		t.Run(streamTest.name+" records only when the RPC ends", func(t *testing.T) {
			gate := fixture.streamService.gate(streamTest.fullMethod)
			finish := streamTest.start(ctx, t, fixture)
			method := strings.TrimPrefix(streamTest.fullMethod, "/")

			metrics := collectA66Metrics(t, fixture.reader)
			for _, name := range a66DurationMetricNames {
				if got := a66HistogramCount(t, metrics, name, map[string]string{"grpc.method": method}); got != 0 {
					t.Fatalf("%s count before stream completion = %d, want 0", name, got)
				}
			}

			gate.release()
			finish(t)
			for _, name := range a66DurationMetricNames {
				waitForA66Count(t, fixture.reader, name, map[string]string{
					"grpc.method": method,
					"grpc.status": "OK",
				}, 1)
			}
		})
	}

	t.Run("unregistered dynamic method is other", func(t *testing.T) {
		err := fixture.conn.Invoke(
			ctx,
			"/dynamic.v1.Runtime/Call",
			&pb.HelloRequest{Name: "unknown"},
			&pb.HelloReply{},
		)
		if status.Code(err) != codes.Unimplemented {
			t.Fatalf("dynamic call code = %v, want %v (err = %v)", status.Code(err), codes.Unimplemented, err)
		}
		for _, name := range a66DurationMetricNames {
			waitForA66Count(t, fixture.reader, name, map[string]string{
				"grpc.method": "other",
				"grpc.status": "UNIMPLEMENTED",
			}, 1)
		}
	})

	t.Run("one logical call has three retry attempts", func(t *testing.T) {
		before := collectA66Metrics(t, fixture.reader)
		beforeCalls := a66HistogramCount(t, before, grpcotel.ClientCallDurationMetricName, nil)
		beforeAttempts := a66HistogramCount(t, before, grpcotel.ClientAttemptDurationMetricName, nil)
		beforeServerCalls := a66HistogramCount(t, before, grpcotel.ServerCallDurationMetricName, nil)

		if _, err := fixture.client.SayHello(ctx, &pb.HelloRequest{Name: "retry"}); err != nil {
			t.Fatalf("SayHello(retry) failed: %v", err)
		}
		if got := fixture.service.retryAttempts.Load(); got != 3 {
			t.Fatalf("server retry attempts = %d, want 3", got)
		}

		waitForA66Count(t, fixture.reader, grpcotel.ClientCallDurationMetricName, nil, beforeCalls+1)
		waitForA66Count(t, fixture.reader, grpcotel.ClientAttemptDurationMetricName, nil, beforeAttempts+3)
		waitForA66Count(t, fixture.reader, grpcotel.ServerCallDurationMetricName, nil, beforeServerCalls+3)

		method := "helloworld.Greeter/SayHello"
		metrics := collectA66Metrics(t, fixture.reader)
		if got := a66HistogramCount(t, metrics, grpcotel.ClientAttemptDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "UNAVAILABLE",
		}); got != 2 {
			t.Fatalf("unavailable client attempts = %d, want 2", got)
		}
		if got := a66HistogramCount(t, metrics, grpcotel.ServerCallDurationMetricName, map[string]string{
			"grpc.method": method,
			"grpc.status": "UNAVAILABLE",
		}); got != 2 {
			t.Fatalf("unavailable server calls = %d, want 2", got)
		}
	})

	assertA66DurationOnlyContract(t, collectA66Metrics(t, fixture.reader))
}

func TestGRPCA66MeterProviderIsolation(t *testing.T) {
	first := newA66Fixture(t)
	second := newA66Fixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if _, err := first.client.SayHello(ctx, &pb.HelloRequest{Name: "first"}); err != nil {
		t.Fatalf("first SayHello() failed: %v", err)
	}
	waitForA66Count(t, first.reader, grpcotel.ClientCallDurationMetricName, nil, 1)
	if got := a66HistogramCount(t, collectA66Metrics(t, second.reader), grpcotel.ClientCallDurationMetricName, nil); got != 0 {
		t.Fatalf("second provider recorded first fixture call: count = %d", got)
	}

	if _, err := second.client.SayHello(ctx, &pb.HelloRequest{Name: "second"}); err != nil {
		t.Fatalf("second SayHello() failed: %v", err)
	}
	waitForA66Count(t, second.reader, grpcotel.ClientCallDurationMetricName, nil, 1)
	if got := a66HistogramCount(t, collectA66Metrics(t, first.reader), grpcotel.ClientCallDurationMetricName, nil); got != 1 {
		t.Fatalf("first provider count after second fixture call = %d, want 1", got)
	}
}

func TestGRPCA66ZeroTraceOptionsDoesNotDuplicateClientSpan(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down tracer provider: %v", err)
		}
	})

	fixture := newA66FixtureWithProvider(
		t,
		nil,
		metricnoop.NewMeterProvider(),
		forgegrpc.WithClientMiddleware(forgetracing.Client(
			forgetracing.WithTracerProvider(tracerProvider),
		)),
	)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, err := fixture.client.SayHello(ctx, &pb.HelloRequest{Name: "traced"}); err != nil {
		t.Fatalf("SayHello(traced) failed: %v", err)
	}

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended span count = %d, want exactly 1", len(ended))
	}
	if got := ended[0].SpanKind(); got != trace.SpanKindClient {
		t.Fatalf("span kind = %v, want %v", got, trace.SpanKindClient)
	}
	if got := ended[0].Name(); got != pb.Greeter_SayHello_FullMethodName {
		t.Fatalf("span name = %q, want %q", got, pb.Greeter_SayHello_FullMethodName)
	}
}

type a66Fixture struct {
	client        pb.GreeterClient
	conn          *grpc.ClientConn
	reader        *sdkmetric.ManualReader
	service       *a66GreeterServer
	streamService *a66StreamService
}

func newA66Fixture(t *testing.T) *a66Fixture {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutting down meter provider: %v", err)
		}
	})
	return newA66FixtureWithProvider(t, reader, provider)
}

func newA66FixtureWithProvider(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	provider otelmetric.MeterProvider,
	extraClientOptions ...forgegrpc.ClientOption,
) *a66Fixture {
	// Each fixture owns its server, client, and optional metric reader.
	t.Helper()

	otelOptions := a66DurationOnlyOptions(provider)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() failed: %v", err)
	}
	service := newA66GreeterServer()
	streamService := newA66StreamService()
	server := forgegrpc.NewServer(
		forgegrpc.WithListener(listener),
		forgegrpc.WithTimeout(10*time.Second),
		forgegrpc.WithCustomHealth(),
		forgegrpc.WithDisableReflection(),
		forgegrpc.WithOptions(
			grpcotel.ServerOption(otelOptions),
			grpc.UnknownServiceHandler(func(any, grpc.ServerStream) error {
				return status.Error(codes.Unimplemented, "unknown method")
			}),
		),
	)
	pb.RegisterGreeterServer(server, service)
	server.RegisterService(&a66StreamServiceDesc, streamService)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Start(t.Context())
	}()
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if stopErr := server.Stop(stopCtx); stopErr != nil {
			t.Errorf("stopping gRPC server: %v", stopErr)
		}
		select {
		case serveResult := <-serveErr:
			if serveResult != nil && !errors.Is(serveResult, grpc.ErrServerStopped) && !errors.Is(serveResult, net.ErrClosed) {
				t.Errorf("gRPC server returned: %v", serveResult)
			}
		case <-stopCtx.Done():
			t.Errorf("waiting for gRPC server shutdown: %v", stopCtx.Err())
		}
	})

	clientOptions := make([]forgegrpc.ClientOption, 0, 4+len(extraClientOptions))
	clientOptions = append(clientOptions,
		forgegrpc.WithTarget(listener.Addr().String()),
		forgegrpc.WithRequestTimeout(10*time.Second),
		forgegrpc.WithHealthCheck(false),
		forgegrpc.WithDialOptions(
			grpcotel.DialOption(otelOptions),
			grpc.WithDefaultServiceConfig(a66RetryServiceConfig),
		),
	)
	clientOptions = append(clientOptions, extraClientOptions...)
	conn, err := forgegrpc.NewClient(t.Context(), clientOptions...)
	if err != nil {
		t.Fatalf("forgegrpc.NewClient() failed: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("closing gRPC client: %v", err)
		}
	})
	t.Cleanup(streamService.releaseAll)

	return &a66Fixture{
		client:        pb.NewGreeterClient(conn),
		conn:          conn,
		reader:        reader,
		service:       service,
		streamService: streamService,
	}
}

func a66DurationOnlyOptions(provider otelmetric.MeterProvider) grpcotel.Options {
	return grpcotel.Options{
		MetricsOptions: grpcotel.MetricsOptions{
			MeterProvider: provider,
			Metrics:       grpcstats.NewMetricSet(a66DurationMetricNames...),
		},
	}
}

type a66GreeterServer struct {
	pb.UnimplementedGreeterServer

	retryAttempts atomic.Int32
}

func newA66GreeterServer() *a66GreeterServer {
	return &a66GreeterServer{}
}

func (s *a66GreeterServer) SayHello(_ context.Context, request *pb.HelloRequest) (*pb.HelloReply, error) {
	switch request.Name {
	case "error":
		return nil, status.Error(codes.InvalidArgument, "invalid greeting")
	case "retry":
		if s.retryAttempts.Add(1) < 3 {
			return nil, status.Error(codes.Unavailable, "retry greeting")
		}
	}
	return &pb.HelloReply{Message: "hello " + request.Name}, nil
}

type a66StreamService struct {
	gates map[string]*a66StreamGate
}

func newA66StreamService() *a66StreamService {
	gates := make(map[string]*a66StreamGate, 4)
	for _, method := range []string{
		a66ClientStreamingFullMethodName,
		a66ServerStreamingFullMethodName,
		a66FullDuplexFullMethodName,
		a66HalfDuplexFullMethodName,
	} {
		gates[method] = newA66StreamGate()
	}
	return &a66StreamService{gates: gates}
}

func (s *a66StreamService) clientStreaming(stream grpc.ServerStream) error {
	request := new(pb.HelloRequest)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	gate := s.gate(a66ClientStreamingFullMethodName)
	gate.markStarted()
	if err := gate.wait(stream.Context()); err != nil {
		return err
	}
	return stream.SendMsg(&pb.HelloReply{Message: request.Name})
}

func (s *a66StreamService) serverStreaming(stream grpc.ServerStream) error {
	request := new(pb.HelloRequest)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	if err := stream.SendMsg(&pb.HelloReply{Message: request.Name}); err != nil {
		return err
	}
	gate := s.gate(a66ServerStreamingFullMethodName)
	gate.markStarted()
	return gate.wait(stream.Context())
}

func (s *a66StreamService) fullDuplex(stream grpc.ServerStream) error {
	request := new(pb.HelloRequest)
	if err := stream.RecvMsg(request); err != nil {
		return err
	}
	if err := stream.SendMsg(&pb.HelloReply{Message: request.Name}); err != nil {
		return err
	}
	gate := s.gate(a66FullDuplexFullMethodName)
	gate.markStarted()
	return gate.wait(stream.Context())
}

func (s *a66StreamService) halfDuplex(stream grpc.ServerStream) error {
	var request pb.HelloRequest
	for {
		if err := stream.RecvMsg(&request); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return err
		}
	}
	if err := stream.SendMsg(&pb.HelloReply{Message: request.Name}); err != nil {
		return err
	}
	gate := s.gate(a66HalfDuplexFullMethodName)
	gate.markStarted()
	return gate.wait(stream.Context())
}

type a66StreamServiceServer interface {
	clientStreaming(grpc.ServerStream) error
	serverStreaming(grpc.ServerStream) error
	fullDuplex(grpc.ServerStream) error
	halfDuplex(grpc.ServerStream) error
}

var a66StreamServiceDesc = grpc.ServiceDesc{
	ServiceName: "sylphy.metrics.v1.StreamService",
	HandlerType: (*a66StreamServiceServer)(nil),
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "ClientStreaming",
			Handler:       a66ClientStreamingHandler,
			ClientStreams: true,
		},
		{
			StreamName:    "ServerStreaming",
			Handler:       a66ServerStreamingHandler,
			ServerStreams: true,
		},
		{
			StreamName:    "FullDuplex",
			Handler:       a66FullDuplexHandler,
			ServerStreams: true,
			ClientStreams: true,
		},
		{
			StreamName:    "HalfDuplex",
			Handler:       a66HalfDuplexHandler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "grpc_a66_test.go",
}

func a66ClientStreamingHandler(service any, stream grpc.ServerStream) error {
	return service.(a66StreamServiceServer).clientStreaming(stream)
}

func a66ServerStreamingHandler(service any, stream grpc.ServerStream) error {
	return service.(a66StreamServiceServer).serverStreaming(stream)
}

func a66FullDuplexHandler(service any, stream grpc.ServerStream) error {
	return service.(a66StreamServiceServer).fullDuplex(stream)
}

func a66HalfDuplexHandler(service any, stream grpc.ServerStream) error {
	return service.(a66StreamServiceServer).halfDuplex(stream)
}

func (s *a66StreamService) gate(method string) *a66StreamGate {
	gate, ok := s.gates[method]
	if !ok {
		panic("missing stream gate for " + method)
	}
	return gate
}

func (s *a66StreamService) releaseAll() {
	for _, gate := range s.gates {
		gate.release()
	}
}

type a66StreamGate struct {
	started     chan struct{}
	released    chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newA66StreamGate() *a66StreamGate {
	return &a66StreamGate{
		started:  make(chan struct{}),
		released: make(chan struct{}),
	}
}

func (g *a66StreamGate) markStarted() {
	g.startedOnce.Do(func() { close(g.started) })
}

func (g *a66StreamGate) wait(ctx context.Context) error {
	select {
	case <-g.released:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *a66StreamGate) waitUntilStarted(ctx context.Context, t *testing.T) {
	t.Helper()
	select {
	case <-g.started:
	case <-ctx.Done():
		t.Fatalf("waiting for stream handler: %v", ctx.Err())
	}
}

func (g *a66StreamGate) release() {
	g.releaseOnce.Do(func() { close(g.released) })
}

func startA66ClientStreamingCall(ctx context.Context, t *testing.T, fixture *a66Fixture) func(*testing.T) {
	t.Helper()
	stream := newA66ClientStream(
		ctx,
		t,
		fixture,
		a66ClientStreamingStreamIndex,
		a66ClientStreamingFullMethodName,
	)
	if err := stream.SendMsg(&pb.HelloRequest{Name: "client streaming"}); err != nil {
		t.Fatalf("stream.Send() failed: %v", err)
	}
	finished := make(chan error, 1)
	go func() {
		err := stream.CloseSend()
		if err == nil {
			err = stream.RecvMsg(new(pb.HelloReply))
		}
		finished <- err
	}()
	fixture.streamService.gate(a66ClientStreamingFullMethodName).waitUntilStarted(ctx, t)
	return func(t *testing.T) {
		t.Helper()
		select {
		case err := <-finished:
			if err != nil {
				t.Fatalf("stream.CloseAndRecv() failed: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("waiting for client stream completion: %v", ctx.Err())
		}
	}
}

func startA66ServerStreamingCall(ctx context.Context, t *testing.T, fixture *a66Fixture) func(*testing.T) {
	t.Helper()
	stream := newA66ClientStream(
		ctx,
		t,
		fixture,
		a66ServerStreamingStreamIndex,
		a66ServerStreamingFullMethodName,
	)
	if err := stream.SendMsg(&pb.HelloRequest{Name: "server streaming"}); err != nil {
		t.Fatalf("stream.Send() failed: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v", err)
	}
	fixture.streamService.gate(a66ServerStreamingFullMethodName).waitUntilStarted(ctx, t)
	if err := stream.RecvMsg(new(pb.HelloReply)); err != nil {
		t.Fatalf("stream.Recv() failed before completion: %v", err)
	}
	return func(t *testing.T) {
		t.Helper()
		if err := stream.RecvMsg(new(pb.HelloReply)); !errors.Is(err, io.EOF) {
			t.Fatalf("stream.Recv() final error = %v, want EOF", err)
		}
	}
}

func startA66FullDuplexCall(ctx context.Context, t *testing.T, fixture *a66Fixture) func(*testing.T) {
	t.Helper()
	stream := newA66ClientStream(
		ctx,
		t,
		fixture,
		a66FullDuplexStreamIndex,
		a66FullDuplexFullMethodName,
	)
	if err := stream.SendMsg(&pb.HelloRequest{Name: "full duplex"}); err != nil {
		t.Fatalf("stream.Send() failed: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v", err)
	}
	fixture.streamService.gate(a66FullDuplexFullMethodName).waitUntilStarted(ctx, t)
	if err := stream.RecvMsg(new(pb.HelloReply)); err != nil {
		t.Fatalf("stream.Recv() failed before completion: %v", err)
	}
	return func(t *testing.T) {
		t.Helper()
		if err := stream.RecvMsg(new(pb.HelloReply)); !errors.Is(err, io.EOF) {
			t.Fatalf("stream.Recv() final error = %v, want EOF", err)
		}
	}
}

func startA66HalfDuplexCall(ctx context.Context, t *testing.T, fixture *a66Fixture) func(*testing.T) {
	t.Helper()
	stream := newA66ClientStream(
		ctx,
		t,
		fixture,
		a66HalfDuplexStreamIndex,
		a66HalfDuplexFullMethodName,
	)
	if err := stream.SendMsg(&pb.HelloRequest{Name: "half duplex"}); err != nil {
		t.Fatalf("stream.Send() failed: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatalf("stream.CloseSend() failed: %v", err)
	}
	fixture.streamService.gate(a66HalfDuplexFullMethodName).waitUntilStarted(ctx, t)
	if err := stream.RecvMsg(new(pb.HelloReply)); err != nil {
		t.Fatalf("stream.Recv() failed before completion: %v", err)
	}
	return func(t *testing.T) {
		t.Helper()
		if err := stream.RecvMsg(new(pb.HelloReply)); !errors.Is(err, io.EOF) {
			t.Fatalf("stream.Recv() final error = %v, want EOF", err)
		}
	}
}

func newA66ClientStream(ctx context.Context, t *testing.T, fixture *a66Fixture, streamIndex int, fullMethod string) grpc.ClientStream {
	t.Helper()

	stream, err := fixture.conn.NewStream(
		ctx,
		&a66StreamServiceDesc.Streams[streamIndex],
		fullMethod,
		grpc.StaticMethod(),
	)
	if err != nil {
		t.Fatalf("NewStream(%s) failed: %v", fullMethod, err)
	}
	return stream
}

func collectA66Metrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()

	var resources metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resources); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}
	metrics := make(map[string]metricdata.Metrics)
	for _, scope := range resources.ScopeMetrics {
		for _, current := range scope.Metrics {
			metrics[current.Name] = current
		}
	}
	return metrics
}

func waitForA66Count(t *testing.T, reader *sdkmetric.ManualReader, name string, wantAttributes map[string]string, want uint64) {
	t.Helper()

	metrics := collectA66Metrics(t, reader)
	got := a66HistogramCount(t, metrics, name, wantAttributes)
	if got != want {
		t.Fatalf("%s count with attributes %v = %d, want %d; metric = %#v", name, wantAttributes, got, want, metrics[name])
	}
}

func a66HistogramCount(t *testing.T, metrics map[string]metricdata.Metrics, name string, wantAttributes map[string]string) uint64 {
	t.Helper()

	current, ok := metrics[name]
	if !ok {
		return 0
	}
	histogram, ok := current.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s data type = %T, want metricdata.Histogram[float64]", name, current.Data)
	}
	var count uint64
	for _, point := range histogram.DataPoints {
		if a66AttributesMatch(point.Attributes, wantAttributes) {
			count += point.Count
		}
	}
	return count
}

func a66AttributesMatch(got attribute.Set, want map[string]string) bool {
	for key, value := range want {
		gotValue, ok := got.Value(attribute.Key(key))
		if !ok || gotValue.AsString() != value {
			return false
		}
	}
	return true
}

func assertA66DurationOnlyContract(t *testing.T, metrics map[string]metricdata.Metrics) {
	t.Helper()

	gotNames := make([]string, 0, len(metrics))
	for name := range metrics {
		gotNames = append(gotNames, name)
	}
	slices.Sort(gotNames)
	wantNames := slices.Clone(a66DurationMetricNames)
	slices.Sort(wantNames)
	if !slices.Equal(gotNames, wantNames) {
		t.Fatalf("metric names = %v, want only %v", gotNames, wantNames)
	}

	for name, current := range metrics {
		if strings.HasPrefix(name, "rpc.") || strings.Contains(name, ".started") || strings.Contains(name, "message_size") {
			t.Errorf("unexpected non-duration metric %q", name)
		}
		if current.Unit != "s" {
			t.Errorf("%s unit = %q, want %q", name, current.Unit, "s")
		}
		histogram, ok := current.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Errorf("%s data type = %T, want metricdata.Histogram[float64]", name, current.Data)
			continue
		}

		wantKeys := []string{"grpc.method", "grpc.status"}
		if name != grpcotel.ServerCallDurationMetricName {
			wantKeys = append(wantKeys, "grpc.target")
		}
		slices.Sort(wantKeys)
		for _, point := range histogram.DataPoints {
			gotKeys := make([]string, 0, point.Attributes.Len())
			for _, value := range point.Attributes.ToSlice() {
				gotKeys = append(gotKeys, string(value.Key))
			}
			slices.Sort(gotKeys)
			if !slices.Equal(gotKeys, wantKeys) {
				t.Errorf("%s attribute keys = %v, want %v", name, gotKeys, wantKeys)
			}
		}
	}
}
