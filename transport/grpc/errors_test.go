package grpc

import (
	"bytes"
	stderrors "errors"
	"fmt"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/errors"
)

var errSentinel = errors.MustDefine(errors.KindNotFound, "test.v1", "SESSION_NOT_FOUND")

type causeError struct{ msg string }

func (e *causeError) Error() string { return e.msg }

// Kind is the single source of truth, so the projection is one-way and total.
// A Kind that projected onto a code recovering as a different Kind would
// reintroduce the loss this design exists to remove.
func TestKindProjectionIsLossless(t *testing.T) {
	for k := errors.KindUnknown; k <= errors.KindDataLoss; k++ {
		if got := KindOf(CodeOf(k)); got != k {
			t.Errorf("round trip: %v -> %v -> %v", k, CodeOf(k), got)
		}
	}
}

// Identity must survive the boundary so a caller matches a remote error against
// the same sentinel it would use locally.
func TestStatusRoundTripPreservesIdentity(t *testing.T) {
	original := errSentinel.
		Msg("session is gone").
		Meta("tenant", "acme").
		WithTraceID("trace-abc")

	received, ok := ErrorFrom(StatusFrom(errors.PublicOf(original)).Err())
	if !ok {
		t.Fatal("ErrorFrom did not recognize a gRPC status")
	}
	if got := received.Kind(); got != errors.KindNotFound {
		t.Errorf("kind = %v, want KindNotFound", got)
	}
	if got := received.Reason(); got != "SESSION_NOT_FOUND" {
		t.Errorf("reason = %q", got)
	}
	if got := received.Domain(); got != "test.v1" {
		t.Errorf("domain = %q", got)
	}
	if got := received.Metadata()["tenant"]; got != "acme" {
		t.Errorf("metadata[tenant] = %q", got)
	}
	if got := received.TraceID(); got != "trace-abc" {
		t.Errorf("trace ID = %q, want it to survive", got)
	}
	if !stderrors.Is(received, errSentinel) {
		t.Error("received error does not match the sentinel that produced it")
	}
}

func TestTraceUsesRequestInfoWithoutConsumingMetadata(t *testing.T) {
	original := errSentinel.
		Meta("trace_id", "application-value").
		WithTraceID("trace-abc")

	gs := StatusFrom(errors.PublicOf(original))
	var (
		requestInfo *errdetails.RequestInfo
		errorInfo   *errdetails.ErrorInfo
	)
	for _, detail := range gs.Details() {
		switch d := detail.(type) {
		case *errdetails.RequestInfo:
			requestInfo = d
		case *errdetails.ErrorInfo:
			errorInfo = d
		}
	}
	if requestInfo == nil || requestInfo.GetRequestId() != "trace-abc" {
		t.Fatalf("RequestInfo = %v, want trace-abc", requestInfo)
	}
	if errorInfo == nil || errorInfo.GetMetadata()["trace_id"] != "application-value" {
		t.Fatalf("ErrorInfo metadata = %v, want application trace_id preserved", errorInfo)
	}

	received, ok := ErrorFrom(gs.Err())
	if !ok {
		t.Fatal("ErrorFrom did not recognize the status")
	}
	if received.TraceID() != "trace-abc" {
		t.Errorf("trace ID = %q, want trace-abc", received.TraceID())
	}
	if received.Metadata()["trace_id"] != "application-value" {
		t.Errorf("metadata trace_id = %q, want application-value", received.Metadata()["trace_id"])
	}
}

func TestStatusDoesNotAttachEmptyRetryInfo(t *testing.T) {
	gs := StatusFrom(errors.PublicOf(errors.Of(errors.KindUnavailable)))
	for _, detail := range gs.Details() {
		if _, ok := detail.(*errdetails.RetryInfo); ok {
			t.Error("status attached RetryInfo without a retry_delay")
		}
	}
}

// The cause chain is local by construction: it routinely holds connection
// strings, so it must not cross a boundary.
func TestCauseDoesNotCrossTheWire(t *testing.T) {
	secret := &causeError{msg: "dial tcp 10.0.0.1:5432: password=hunter2"}
	original := errSentinel.Msg("lookup failed").Wrap(secret)

	raw, err := proto.Marshal(StatusFrom(errors.PublicOf(original)).Proto())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Error("the cause was serialized onto the wire")
	}

	received, _ := ErrorFrom(StatusFrom(errors.PublicOf(original)).Err())
	if received.Unwrap() != nil {
		t.Error("a received error must not carry a cause chain")
	}
	var target *causeError
	if stderrors.As(received, &target) {
		t.Error("As reached a local type through a remote error")
	}
	if !received.IsRemote() {
		t.Error("a received error must be marked remote")
	}
}

// The wire size must not grow with the depth of the cause chain: gRPC's default
// trailer limit is 8 KB, and a status that grew with the chain would eventually
// fail the RPC rather than degrade.
func TestWireSizeIsIndependentOfCauseDepth(t *testing.T) {
	sizeOf := func(err *errors.Error) int {
		t.Helper()
		raw, marshalErr := proto.Marshal(StatusFrom(errors.PublicOf(err)).Proto())
		if marshalErr != nil {
			t.Fatalf("marshal: %v", marshalErr)
		}
		return len(raw)
	}

	shallow := errSentinel.Msg("gone").Wrap(&causeError{msg: "root"})

	var deepCause error = &causeError{msg: "root"}
	for i := range 500 {
		deepCause = fmt.Errorf("layer %d: %w", i, deepCause)
	}
	deep := errSentinel.Msg("gone").Wrap(deepCause)

	if got, want := sizeOf(deep), sizeOf(shallow); got != want {
		t.Errorf("wire size = %d for a 500-deep chain, want %d as for a shallow one", got, want)
	}
	const grpcTrailerLimit = 8 << 10
	if encoded := sizeOf(deep) * 4 / 3; encoded > grpcTrailerLimit {
		t.Errorf("encoded status = %d bytes, over the %d byte trailer limit", encoded, grpcTrailerLimit)
	}
}

// Every violation must survive, so a client can show a user each bad field.
// The aggregate carries a declared identity, as the validate middleware's
// aggregates do; an undeclared aggregate would be withheld at the boundary.
func TestViolationsSurviveTheWire(t *testing.T) {
	var v errors.Violations
	v.Add("email", "malformed")
	v.Add("age", "must be positive")
	err := errors.FromError(v.Err(errors.KindInvalidArgument)).
		WithDomain("test.v1").
		WithReason("VALIDATION_FAILED")
	_ = errors.MustDefine(errors.KindInvalidArgument, "test.v1", "VALIDATION_FAILED")

	received, _ := ErrorFrom(StatusFrom(errors.PublicOf(err)).Err())
	got := received.Violations()
	if len(got) != 2 {
		t.Fatalf("violations = %d, want 2", len(got))
	}
	if got[0].Field != "email" || got[1].Field != "age" {
		t.Errorf("violations lost order or content: %+v", got)
	}
}

// Deciding whether to retry starts from the Kind, so the Kind must survive the
// boundary intact rather than collapsing into a generic failure. A caller
// combines it with its own idempotence declaration; this test covers the half
// the transport is responsible for.
func TestKindSurvivesTheWire(t *testing.T) {
	transient := errors.MustDefine(errors.KindUnavailable, "test.v1", "BACKEND_DOWN")
	received, _ := ErrorFrom(StatusFrom(errors.PublicOf(transient)).Err())
	if got := errors.KindOf(received); got != errors.KindUnavailable {
		t.Errorf("KindOf(received) = %v, want KindUnavailable", got)
	}

	received, _ = ErrorFrom(StatusFrom(errors.PublicOf(errSentinel.Msg("gone"))).Err())
	if got := errors.KindOf(received); got != errors.KindOf(errSentinel) {
		t.Errorf("KindOf(received) = %v, want %v", got, errors.KindOf(errSentinel))
	}
}

// An error from a peer that knows nothing of Forge must still classify.
func TestForeignStatusError(t *testing.T) {
	foreign := status.Error(codes.PermissionDenied, "nope")
	converted, ok := ErrorFrom(foreign)
	if !ok {
		t.Fatal("ErrorFrom did not recognize a foreign status")
	}
	if converted.Kind() != errors.KindPermissionDenied {
		t.Errorf("kind = %v, want KindPermissionDenied", converted.Kind())
	}
	if converted.Message() != "nope" {
		t.Errorf("message = %q", converted.Message())
	}
}

// grpc-go recognizes an error only by the exact GRPCStatus method, so the
// transport must attach one on the way out.
func TestOutgoingErrorCarriesStatus(t *testing.T) {
	srv := NewServer()
	out := srv.projectError(errSentinel.Msg("gone"))

	gs, ok := status.FromError(out)
	if !ok {
		t.Fatal("grpc-go did not recognize the outgoing error")
	}
	if gs.Code() != codes.NotFound {
		t.Errorf("code = %v, want NotFound", gs.Code())
	}
	// The original error stays reachable, so Is and As keep working.
	if !stderrors.Is(out, errSentinel) {
		t.Error("the wrapped error no longer matches its sentinel")
	}
}

func TestConvertClientError(t *testing.T) {
	// A foreign status becomes a Forge error.
	converted := convertClientError(status.Error(codes.NotFound, "gone"))
	if errors.KindOf(converted) != errors.KindNotFound {
		t.Errorf("kind = %v, want KindNotFound", errors.KindOf(converted))
	}
	// An error that is already a Forge error is left alone.
	local := errSentinel.Msg("local")
	if got := convertClientError(local); got != error(local) {
		t.Error("an existing Forge error was re-wrapped")
	}
	// A plain error without a status is left alone.
	plain := stderrors.New("no status")
	if got := convertClientError(plain); got != plain {
		t.Error("a plain error was converted")
	}
	if convertClientError(nil) != nil {
		t.Error("nil must stay nil")
	}
}
