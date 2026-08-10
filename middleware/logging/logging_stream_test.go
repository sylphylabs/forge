package logging

import (
	"context"
	"log/slog"
	"testing"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

func TestServerStreamLogsOnce(t *testing.T) {
	handler := &captureHandler{}
	logger := slog.New(handler)
	tr := &Transport{kind: transport.KindGRPC, operation: "/helloworld.Greeter/SayHelloStream"}
	ctx := transport.NewServerContext(t.Context(), tr)

	next := ServerStream(logger)(func(_ any, stream middleware.ServerStream) error {
		// Several messages must still produce exactly one record.
		for range 3 {
			if err := stream.SendMsg("chunk"); err != nil {
				return err
			}
		}
		return nil
	})
	if err := next(nil, &testStream{ctx: ctx}); err != nil {
		t.Fatal(err)
	}

	if len(handler.records) != 1 {
		t.Fatalf("records len = %d, want 1", len(handler.records))
	}
	attrs := handler.attrs[0]
	if got := attrs["operation"]; got != "/helloworld.Greeter/SayHelloStream" {
		t.Errorf("operation = %v, want %q", got, "/helloworld.Greeter/SayHelloStream")
	}
	if got := attrs["component"]; got != "grpc" {
		t.Errorf("component = %v, want %q", got, "grpc")
	}
	if got := attrs["kind"]; got != "server" {
		t.Errorf("kind = %v, want %q", got, "server")
	}
	if got := attrs["args"]; got != "" {
		t.Errorf("args = %v, want empty for bidirectional streams", got)
	}
	if handler.records[0].Level != slog.LevelInfo {
		t.Errorf("level = %v, want %v", handler.records[0].Level, slog.LevelInfo)
	}
}

func TestServerStreamLogsInitialRequest(t *testing.T) {
	handler := &captureHandler{}
	logger := slog.New(handler)

	next := ServerStream(logger)(func(any, middleware.ServerStream) error { return nil })
	if err := next(&dummyStringer{}, &testStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}

	if got := handler.attrs[0]["args"]; got != "my value" {
		t.Errorf("args = %v, want %q", got, "my value")
	}
}

func TestServerStreamLogsError(t *testing.T) {
	handler := &captureHandler{}
	logger := slog.New(handler)
	want := errors.New(errors.KindInternal).WithReason("STREAM_FAILED").Msg("stream failed")

	next := ServerStream(logger)(func(any, middleware.ServerStream) error { return want })
	if err := next(nil, &testStream{ctx: t.Context()}); err != want {
		t.Fatalf("ServerStream() error = %v, want %v", err, want)
	}

	if handler.records[0].Level != slog.LevelError {
		t.Errorf("level = %v, want %v", handler.records[0].Level, slog.LevelError)
	}
	attrs := handler.attrs[0]
	if got := attrs["reason"]; got != "STREAM_FAILED" {
		t.Errorf("reason = %v, want %q", got, "STREAM_FAILED")
	}
	if got := attrs["error_kind"]; got != "INTERNAL" {
		t.Errorf("error_kind = %v, want INTERNAL", got)
	}
}

func TestServerStreamDisabledLevelSkipsFormatting(t *testing.T) {
	request := &countingRedacter{}
	handler := &captureHandler{disabled: true}
	logger := slog.New(handler)

	next := ServerStream(logger)(func(any, middleware.ServerStream) error { return nil })
	if err := next(request, &testStream{ctx: t.Context()}); err != nil {
		t.Fatal(err)
	}

	if request.calls != 0 {
		t.Errorf("Redact calls = %d, want 0", request.calls)
	}
	if len(handler.records) != 0 {
		t.Errorf("records len = %d, want 0", len(handler.records))
	}
}

func TestServerStreamNilLoggerUsesDefault(t *testing.T) {
	next := ServerStream(nil)(func(any, middleware.ServerStream) error { return nil })
	if err := next(nil, &testStream{ctx: t.Context()}); err != nil {
		t.Errorf("ServerStream() error = %v, want nil", err)
	}
}

type testStream struct {
	ctx context.Context
}

func (s *testStream) Context() context.Context { return s.ctx }
func (*testStream) SendMsg(any) error          { return nil }
func (*testStream) RecvMsg(any) error          { return nil }
