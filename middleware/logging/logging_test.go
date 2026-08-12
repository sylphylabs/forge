package logging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/transport"
)

var _ transport.Transporter = (*Transport)(nil)

type Transport struct {
	kind      transport.Kind
	endpoint  string
	operation string
}

func (tr *Transport) Kind() transport.Kind {
	return tr.kind
}

func (tr *Transport) Endpoint() string {
	return tr.endpoint
}

func (tr *Transport) Operation() string {
	return tr.operation
}

func (tr *Transport) RequestHeader() transport.Header {
	return nil
}

func (tr *Transport) ReplyHeader() transport.Header {
	return nil
}

func TestHTTP(t *testing.T) {
	err := errors.New("reply.error")
	handler := &captureHandler{}
	logger := slog.New(handler)

	tests := []struct {
		name string
		kind func(*slog.Logger) middleware.UnaryMiddleware
		err  error
		ctx  context.Context
		want slog.Level
	}{
		{
			name: "http-server@fail",
			kind: Server,
			err:  err,
			ctx:  transport.NewServerContext(context.Background(), &Transport{kind: transport.KindHTTP, endpoint: "endpoint", operation: "/package.service/method"}),
			want: slog.LevelError,
		},
		{
			name: "http-server@succ",
			kind: Server,
			ctx:  transport.NewServerContext(context.Background(), &Transport{kind: transport.KindHTTP, endpoint: "endpoint", operation: "/package.service/method"}),
			want: slog.LevelInfo,
		},
		{
			name: "http-client@succ",
			kind: Client,
			ctx:  transport.NewClientContext(context.Background(), &Transport{kind: transport.KindHTTP, endpoint: "endpoint", operation: "/package.service/method"}),
			want: slog.LevelInfo,
		},
		{
			name: "http-client@fail",
			kind: Client,
			err:  err,
			ctx:  transport.NewClientContext(context.Background(), &Transport{kind: transport.KindHTTP, endpoint: "endpoint", operation: "/package.service/method"}),
			want: slog.LevelError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler.reset()
			next := func(context.Context, any) (any, error) {
				return "reply", test.err
			}
			next = test.kind(logger)(next)
			reply, gotErr := next(test.ctx, "req.args")
			if reply != "reply" {
				t.Fatalf("reply = %v, want %q", reply, "reply")
			}
			if gotErr != test.err {
				t.Fatalf("err = %v, want %v", gotErr, test.err)
			}
			if len(handler.records) != 1 {
				t.Fatalf("records len = %d, want 1", len(handler.records))
			}
			if handler.records[0].Level != test.want {
				t.Fatalf("level = %v, want %v", handler.records[0].Level, test.want)
			}
			if got := handler.attrs[0]["component"]; got != "http" {
				t.Fatalf("component = %v, want %q", got, "http")
			}
			if got := handler.attrs[0]["operation"]; got != "/package.service/method" {
				t.Fatalf("operation = %v, want %q", got, "/package.service/method")
			}
			if got := handler.attrs[0]["args"]; got != "string" {
				t.Fatalf("args = %v, want %q", got, "string")
			}
			_, hasErrorKind := handler.attrs[0]["error_kind"]
			if test.err == nil && hasErrorKind {
				t.Fatalf("error_kind = %v, want no error attributes on success", handler.attrs[0]["error_kind"])
			}
			if test.err != nil && !hasErrorKind {
				t.Fatal("error_kind missing on failure")
			}
		})
	}
}

func TestDisabledLevelSkipsFormatting(t *testing.T) {
	request := &countingRedacter{}
	handler := &captureHandler{disabled: true}
	logger := slog.New(handler)
	called := false

	next := Server(logger)(func(context.Context, any) (any, error) {
		called = true
		return "reply", nil
	})
	reply, err := next(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if reply != "reply" {
		t.Fatalf("reply = %v, want %q", reply, "reply")
	}
	if !called {
		t.Fatal("business handler was not called")
	}
	if request.calls != 0 {
		t.Fatalf("Redact calls = %d, want 0", request.calls)
	}
	if len(handler.records) != 0 {
		t.Fatalf("records len = %d, want 0", len(handler.records))
	}
}

func BenchmarkServerDisabled(b *testing.B) {
	logger := slog.New(&captureHandler{disabled: true})
	next := Server(logger)(func(context.Context, any) (any, error) {
		return nil, nil
	})
	request := &countingRedacter{}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := next(context.Background(), request); err != nil {
			b.Fatal(err)
		}
	}
	if request.calls != 0 {
		b.Fatalf("Redact calls = %d, want 0", request.calls)
	}
}

type (
	dummy struct {
		field string
	}
	dummyStringer struct {
		field string
	}
	dummyStringerRedacter struct {
		field string
	}
	countingRedacter struct {
		calls int
	}
)

func (r *countingRedacter) Redact() string {
	r.calls++
	return "redacted"
}

func (d *dummyStringer) String() string {
	return "my value"
}

func (d *dummyStringerRedacter) String() string {
	return "my value"
}

func (d *dummyStringerRedacter) Redact() string {
	return "my value redacted"
}

func TestExtractArgs(t *testing.T) {
	tests := []struct {
		name     string
		req      any
		expected string
	}{
		// Only a Redacter discloses content; everything else logs its type.
		{name: "dummyStringer", req: &dummyStringer{field: ""}, expected: "*logging.dummyStringer"},
		{name: "dummy", req: &dummy{field: "value"}, expected: "*logging.dummy"},
		{name: "dummyStringerRedacter", req: &dummyStringerRedacter{field: ""}, expected: "my value redacted"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if value := extractArgs(test.req); value != test.expected {
				t.Errorf(`The stringified %s structure must be equal to "%s", %v given`, test.name, test.expected, value)
			}
		})
	}
}

func TestErrorAttrs(t *testing.T) {
	if attrs := errorAttrs(nil); attrs != nil {
		t.Errorf("errorAttrs(nil) = %v, want nil", attrs)
	}
	attrs := errorAttrs(errors.New("test error"))
	if len(attrs) == 0 {
		t.Fatal("errorAttrs(err) returned no attributes")
	}
	byKey := make(map[string]slog.Attr, len(attrs))
	for _, attr := range attrs {
		byKey[attr.Key] = attr
	}
	if _, ok := byKey["error_kind"]; !ok {
		t.Error("error_kind attribute missing")
	}
	if _, ok := byKey["stack"]; ok {
		t.Error("stack attribute must not be emitted; the value never was a stack")
	}
	errAttr, ok := byKey["error"]
	if !ok {
		t.Fatal("error attribute missing")
	}
	if got := fmt.Sprint(errAttr.Value.Any()); got != "test error" {
		t.Errorf("error = %q, want %q", got, "test error")
	}
}

type captureHandler struct {
	records  []slog.Record
	attrs    []map[string]any
	disabled bool
}

func (h *captureHandler) reset() {
	h.records = nil
	h.attrs = nil
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool {
	return !h.disabled
}

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	h.records = append(h.records, record.Clone())
	h.attrs = append(h.attrs, attrs)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *captureHandler) WithGroup(string) slog.Handler {
	return h
}
