package message_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware"
	"github.com/sylphylabs/forge/middleware/logging"
	"github.com/sylphylabs/forge/middleware/ratelimit"
	"github.com/sylphylabs/forge/middleware/recovery"
	"github.com/sylphylabs/forge/middleware/timeout"
	"github.com/sylphylabs/forge/middleware/validate"
	"github.com/sylphylabs/forge/transport/message"
)

// The point of converging the handler signature is that a message consumer uses
// the framework's middleware rather than a message-specific reimplementation of
// each. These assert that the shared middleware compose onto a message handler
// and behave, which is what the convergence was for.
//
// Before it, a consumer had exactly one middleware available — an OpenTelemetry
// tracer in contrib — so a panicking handler took the whole worker down.

func TestRecoveryContainsAPanickingHandler(t *testing.T) {
	handler := recovery.Recovery()(func(context.Context, any) (any, error) {
		panic("malformed payload")
	})

	_, err := handler(t.Context(), message.New([]byte("body")))
	if err == nil {
		t.Fatal("a panicking handler must surface an error, not unwind the worker")
	}
	if !errors.Is(err, recovery.ErrUnknownRequest) {
		t.Errorf("error = %v, want the recovery sentinel", err)
	}
}

func TestLoggingRecordsAMessageFailure(t *testing.T) {
	var recorded bool
	logger := slog.New(recordingHandler{onRecord: func() { recorded = true }})

	wantErr := errors.New("handler failed")
	handler := logging.Server(logger)(func(context.Context, any) (any, error) {
		return nil, wantErr
	})

	if _, err := handler(t.Context(), message.New([]byte("body"))); !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want it passed through", err)
	}
	if !recorded {
		t.Error("logging middleware produced no record for a message failure")
	}
}

func TestRateLimitRejectsBeyondBudget(t *testing.T) {
	handler := ratelimit.Server(ratelimit.WithLimiter(alwaysLimited{}))(
		func(context.Context, any) (any, error) { return nil, nil })

	if _, err := handler(t.Context(), message.New([]byte("body"))); !errors.Is(err, ratelimit.ErrLimitExceed) {
		t.Errorf("error = %v, want ErrLimitExceed", err)
	}
}

func TestTimeoutAppliesToAMessageHandler(t *testing.T) {
	handler := timeout.Server(timeout.WithTimeout(1))(
		func(ctx context.Context, _ any) (any, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})

	if _, err := handler(t.Context(), message.New([]byte("body"))); err == nil {
		t.Error("a handler that outlives its deadline must fail")
	}
}

// Validation reaches the decoded value, so a consumer rejects a malformed
// message with the same middleware an HTTP handler uses.
func TestValidateRejectsAnInvalidMessage(t *testing.T) {
	wantErr := errors.New("invalid")
	handler := validate.Validator(func(any) error { return wantErr })(
		func(context.Context, any) (any, error) { return nil, nil })

	_, err := handler(t.Context(), message.New([]byte("body")))
	if forgeerrors.KindOf(err) != forgeerrors.KindInvalidArgument {
		t.Errorf("kind = %v, want KindInvalidArgument", forgeerrors.KindOf(err))
	}
}

// Every middleware must compose onto one handler, which is the arrangement a
// real consumer uses.
func TestMiddlewareStackComposes(t *testing.T) {
	logger := slog.New(recordingHandler{onRecord: func() {}})
	handler := middleware.ChainUnary(
		recovery.Recovery(),
		logging.Server(logger),
		timeout.Server(timeout.WithTimeout(0)),
	)(func(context.Context, any) (any, error) { return nil, nil })

	if _, err := handler(t.Context(), message.New([]byte("body"))); err != nil {
		t.Errorf("stacked middleware failed a good message: %v", err)
	}
}

type recordingHandler struct {
	slog.Handler
	onRecord func()
}

func (h recordingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordingHandler) Handle(context.Context, slog.Record) error {
	h.onRecord()
	return nil
}
func (h recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h recordingHandler) WithGroup(string) slog.Handler      { return h }

type alwaysLimited struct{}

func (alwaysLimited) Allow() (ratelimit.DoneFunc, error) { return nil, errors.New("limited") }
