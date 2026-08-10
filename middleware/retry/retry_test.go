package retry

import (
	"context"
	stderrors "errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/middleware/governance"
	"github.com/sylphylabs/forge/transport"
)

var _ transport.Transporter = (*testTransport)(nil)

type testTransport struct {
	operation string
	request   *http.Request
}

func (tr *testTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (tr *testTransport) Endpoint() string                { return "" }
func (tr *testTransport) Operation() string               { return tr.operation }
func (tr *testTransport) RequestHeader() transport.Header { return nil }
func (tr *testTransport) Request() *http.Request          { return tr.request }

func clientCtx(operation string) context.Context {
	return transport.NewClientContext(context.Background(), &testTransport{operation: operation})
}

// noSleep replaces the real sleeper, recording each wait.
func noSleep(waits *[]time.Duration) Option {
	return func(o *options) {
		o.sleep = func(ctx context.Context, d time.Duration) error {
			if waits != nil {
				*waits = append(*waits, d)
			}
			return ctx.Err()
		}
	}
}

// maxRand makes every draw hit the top of the jitter range, so waits equal
// their bounds minus one and the backoff curve becomes observable.
func maxRand() Option {
	return func(o *options) {
		o.randN = func(n int64) int64 { return n - 1 }
	}
}

func mustClientNext(t *testing.T, next func(context.Context, any) (any, error), opts ...Option) func(context.Context, any) (any, error) {
	t.Helper()
	m, err := Client(opts...)
	if err != nil {
		t.Fatalf("Client: %v", err)
	}
	if next == nil {
		next = func(context.Context, any) (any, error) { return "ok", nil }
	}
	return m(next)
}

// notSentErr is an error carrying the transport's proof that the request never
// reached a server. [DefaultRetryable] retries it without any idempotence
// declaration, which makes it the neutral fixture for tests about retry
// mechanics — attempt counting, backoff curves, rule tables — that are not
// about the retry decision itself.
func notSentErr() error {
	return transport.MarkNotSent(errors.New(errors.KindUnavailable).WithReason("UNAVAILABLE").Msg("down"))
}

func failNTimes(n int, err error, calls *int) func(context.Context, any) (any, error) {
	return func(context.Context, any) (any, error) {
		*calls++
		if *calls <= n {
			return nil, err
		}
		return "ok", nil
	}
}

func TestRetrySucceedsAfterTransientFailures(t *testing.T) {
	var calls int
	h := mustClientNext(t, failNTimes(2, notSentErr(), &calls), noSleep(nil))
	reply, err := h(context.Background(), nil)
	if err != nil || reply != "ok" {
		t.Fatalf("want success, got %v, %v", reply, err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestRetryExhaustsAttempts(t *testing.T) {
	var calls int
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls), noSleep(nil))
	if _, err := h(context.Background(), nil); !errors.Is(err, cause) {
		t.Fatalf("want last error, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("default policy allows 3 attempts, got %d", calls)
	}
}

func TestRetryStopsOnNonRetryableError(t *testing.T) {
	var calls int
	cause := errors.New(errors.KindInvalidArgument).WithReason("INVALID").Msg("bad")
	h := mustClientNext(t, failNTimes(99, cause, &calls), noSleep(nil))
	if _, err := h(context.Background(), nil); !errors.Is(err, cause) {
		t.Fatalf("want the error unchanged, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("non-retryable error must not be retried, got %d attempts", calls)
	}
}

func TestRetryStopsWhenContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	cause := notSentErr()
	next := func(context.Context, any) (any, error) {
		calls++
		cancel() // the call fails and the caller has gone away
		return nil, cause
	}
	h := mustClientNext(t, next, noSleep(nil))
	if _, err := h(ctx, nil); !errors.Is(err, cause) {
		t.Fatalf("want last attempt error, not ctx error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("canceled context must stop retries, got %d attempts", calls)
	}
}

func TestRetryGivesUpWhenDeadlineCannotFitBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	var calls int
	cause := notSentErr()
	// Default policy draws the first wait from [0, 100ms); force the top.
	var waits []time.Duration
	h := mustClientNext(t, failNTimes(99, cause, &calls), noSleep(&waits), maxRand())
	if _, err := h(ctx, nil); !errors.Is(err, cause) {
		t.Fatalf("want last error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("a backoff longer than the remaining deadline must abandon the retry, got %d attempts", calls)
	}
	if len(waits) != 0 {
		t.Fatalf("must not sleep when giving up, slept %v", waits)
	}
}

func TestRetryStopsWhenSleepInterrupted(t *testing.T) {
	var calls int
	cause := notSentErr()
	interrupted := func(o *options) {
		o.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	}
	h := mustClientNext(t, failNTimes(99, cause, &calls), interrupted)
	if _, err := h(context.Background(), nil); !errors.Is(err, cause) {
		t.Fatalf("want last error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("interrupted sleep must stop retries, got %d attempts", calls)
	}
}

func TestBackoffCurveIsExponentialWithCap(t *testing.T) {
	var calls int
	var waits []time.Duration
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls),
		WithPolicy(Policy{Attempts: 6, BaseBackoff: 100 * time.Millisecond, MaxBackoff: 500 * time.Millisecond}),
		noSleep(&waits), maxRand())
	if _, err := h(context.Background(), nil); err == nil {
		t.Fatal("want exhaustion")
	}
	// Bounds double from the base and cap at MaxBackoff; maxRand yields
	// bound-1 nanoseconds.
	want := []time.Duration{
		100*time.Millisecond - 1,
		200*time.Millisecond - 1,
		400*time.Millisecond - 1,
		500*time.Millisecond - 1,
		500*time.Millisecond - 1,
	}
	if len(waits) != len(want) {
		t.Fatalf("want %d waits, got %v", len(want), waits)
	}
	for i := range want {
		if waits[i] != want[i] {
			t.Fatalf("wait %d: want %s, got %s", i, want[i], waits[i])
		}
	}
}

func TestBackoffJitterStaysWithinBound(t *testing.T) {
	e := ExponentialJitter{Base: 100 * time.Millisecond, Max: time.Second}
	for attempt, bound := range map[int]time.Duration{
		1: 100 * time.Millisecond,
		2: 200 * time.Millisecond,
		3: 400 * time.Millisecond,
		9: time.Second,
	} {
		if got := e.bound(attempt); got != bound {
			t.Fatalf("attempt %d: want bound %s, got %s", attempt, bound, got)
		}
	}
}

// constantBackoff waits the same interval before every retry — a second real
// [Backoff] implementation, and the shape a caller wants when the dependency
// recovers on a fixed cycle rather than under load-driven contention.
type constantBackoff time.Duration

func (c constantBackoff) Next(int) time.Duration { return time.Duration(c) }

// linearBackoff grows the wait by a fixed step per attempt.
type linearBackoff time.Duration

func (l linearBackoff) Next(attempt int) time.Duration {
	return time.Duration(l) * time.Duration(attempt)
}

func TestWithBackoffReplacesTheCurve(t *testing.T) {
	cause := notSentErr()
	cases := []struct {
		name    string
		backoff Backoff
		want    []time.Duration
	}{
		{"constant", constantBackoff(30 * time.Millisecond), []time.Duration{
			30 * time.Millisecond, 30 * time.Millisecond, 30 * time.Millisecond,
		}},
		{"linear", linearBackoff(20 * time.Millisecond), []time.Duration{
			20 * time.Millisecond, 40 * time.Millisecond, 60 * time.Millisecond,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			var waits []time.Duration
			h := mustClientNext(t, failNTimes(99, cause, &calls),
				WithPolicy(Policy{Attempts: 4, BaseBackoff: time.Hour, MaxBackoff: time.Hour}),
				WithBackoff(tc.backoff), noSleep(&waits), maxRand())
			if _, err := h(context.Background(), nil); err == nil {
				t.Fatal("want exhaustion")
			}
			if calls != 4 {
				t.Fatalf("want 4 attempts, got %d", calls)
			}
			// The injected curve owns the wait outright: neither the
			// policy's backoff fields nor the jitter draw touch it.
			if len(waits) != len(tc.want) {
				t.Fatalf("want %d waits, got %v", len(tc.want), waits)
			}
			for i := range tc.want {
				if waits[i] != tc.want[i] {
					t.Fatalf("wait %d: want %s, got %s", i, tc.want[i], waits[i])
				}
			}
		})
	}
}

func TestWithBackoffIgnoresGovernedBackoffButNotAttempts(t *testing.T) {
	// A rule table still governs Attempts when a curve is injected; its
	// backoff fields no longer shape any wait.
	rules := governance.NewRules(Policy{Attempts: 1})
	var calls int
	var waits []time.Duration
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls),
		WithRules(rules), WithBackoff(constantBackoff(5*time.Millisecond)), noSleep(&waits))

	rules.Replace(map[string]Policy{
		"/svc/Method": {Attempts: 3, BaseBackoff: time.Hour, MaxBackoff: time.Hour},
	})
	if _, err := h(clientCtx("/svc/Method"), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 3 {
		t.Fatalf("governed attempts must still apply, got %d", calls)
	}
	for i, w := range waits {
		if w != 5*time.Millisecond {
			t.Fatalf("wait %d: injected curve must ignore the governed backoff, got %s", i, w)
		}
	}
}

func TestExponentialJitterDrawsWithinBound(t *testing.T) {
	e := ExponentialJitter{Base: 100 * time.Millisecond, Max: 400 * time.Millisecond}
	for attempt := 1; attempt <= 5; attempt++ {
		bound := e.bound(attempt)
		for range 100 {
			if got := e.Next(attempt); got < 0 || got >= bound {
				t.Fatalf("attempt %d: draw %s outside [0, %s)", attempt, got, bound)
			}
		}
	}
}

func TestExponentialJitterZeroValueWaitsNothing(t *testing.T) {
	var e ExponentialJitter
	if got := e.Next(1); got != 0 {
		t.Fatalf("zero value must not wait, got %s", got)
	}
}

func TestDefaultRetryable(t *testing.T) {
	ctx := context.Background()
	unavailable := errors.New(errors.KindUnavailable).WithReason("UNAVAILABLE").Msg("down")
	timeout := errors.New(errors.KindDeadlineExceeded).WithReason("TIMEOUT").Msg("slow")
	dialErr := &net.OpError{Op: "dial", Net: "tcp", Err: stderrors.New("connection refused")}
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"nil error", ctx, nil, false},

		// Evidence alone suffices: work that never started cannot be
		// duplicated by starting it, whatever the Kind or the declaration.
		{"proven undelivered", ctx, transport.MarkNotSent(unavailable), true},
		{"proven undelivered, non-transient kind", ctx, transport.MarkNotSent(errors.New(errors.KindInternal).WithReason("X").Msg("x")), true},
		{"proven undelivered behind a wrap", ctx, errors.New(errors.KindInternal).WithReason("X").Msg("x").Wrap(transport.MarkNotSent(dialErr)), true},

		// Without evidence, an ambiguous failure needs the declaration,
		// because a server may have executed the request already.
		{"unavailable without declaration", ctx, unavailable, false},
		{"unavailable with declaration", Idempotent(ctx), unavailable, true},
		{"timeout without declaration", ctx, timeout, false},
		{"timeout with declaration", Idempotent(ctx), timeout, true},

		// A dial error the transport did not mark carries no evidence here:
		// this package reads the mark, it does not re-derive it.
		{"unmarked dial failure", ctx, dialErr, false},

		// Local abandonment is never worth repeating, declared or not.
		{"local cancellation", Idempotent(ctx), context.Canceled, false},
		{"local deadline", Idempotent(ctx), context.DeadlineExceeded, false},

		// Kinds outside the declared set stay out even when declared.
		{"bad request", Idempotent(ctx), errors.New(errors.KindInvalidArgument).WithReason("INVALID").Msg("bad"), false},
		{"internal error", Idempotent(ctx), errors.New(errors.KindInternal).WithReason("BOOM").Msg("boom"), false},
		{"too many requests", Idempotent(ctx), errors.New(errors.KindResourceExhausted).WithReason("RATELIMIT").Msg("limited"), false},
		{"conflict", Idempotent(ctx), errors.New(errors.KindConflict).WithReason("CONFLICT").Msg("stale"), false},
	}
	for _, tc := range cases {
		if got := DefaultRetryable(tc.ctx, tc.err); got != tc.want {
			t.Errorf("%s: want %v, got %v", tc.name, tc.want, got)
		}
	}
}

// The middleware must act on the predicate's verdict, not merely compute it:
// an undeclared ambiguous failure stops after one attempt, and the same
// failure retries once the caller declares the operation idempotent.
func TestUndeclaredAmbiguousFailureIsNotRetried(t *testing.T) {
	var calls int
	cause := errors.New(errors.KindUnavailable).WithReason("UNAVAILABLE").Msg("down")
	h := mustClientNext(t, failNTimes(99, cause, &calls), noSleep(nil))
	if _, err := h(context.Background(), nil); !errors.Is(err, cause) {
		t.Fatalf("want the error unchanged, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("an unproven, undeclared failure must not be retried, got %d attempts", calls)
	}
}

func TestDeclaredIdempotentFailureIsRetried(t *testing.T) {
	var calls int
	cause := errors.New(errors.KindUnavailable).WithReason("UNAVAILABLE").Msg("down")
	h := mustClientNext(t, failNTimes(2, cause, &calls), noSleep(nil))
	if _, err := h(Idempotent(context.Background()), nil); err != nil {
		t.Fatalf("a declared operation must retry, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestProvenUndeliveredFailureIsRetriedWithoutDeclaration(t *testing.T) {
	var calls int
	h := mustClientNext(t, failNTimes(2, notSentErr(), &calls), noSleep(nil))
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatalf("proven-undelivered must retry without a declaration, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("want 3 attempts, got %d", calls)
	}
}

func TestIsIdempotent(t *testing.T) {
	if IsIdempotent(context.Background()) {
		t.Fatal("plain context must not read as idempotent")
	}
	if !IsIdempotent(Idempotent(context.Background())) {
		t.Fatal("declared context must read as idempotent")
	}
}

func TestWithRetryableOverridesDefault(t *testing.T) {
	var calls int
	cause := errors.New(errors.KindInvalidArgument).WithReason("INVALID").Msg("bad") // not retryable by default
	h := mustClientNext(t, failNTimes(1, cause, &calls),
		WithRetryable(func(_ context.Context, err error) bool { return errors.KindOf(err) == errors.KindInvalidArgument }),
		noSleep(nil))
	if _, err := h(context.Background(), nil); err != nil {
		t.Fatalf("custom predicate must retry, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("want 2 attempts, got %d", calls)
	}
}

func TestWithRulesHotUpdate(t *testing.T) {
	rules := governance.NewRules(Policy{Attempts: 1})
	var calls int
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls), WithRules(rules), noSleep(nil))

	// The construction-time default disables retries.
	if _, err := h(clientCtx("/svc/Method"), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 1 {
		t.Fatalf("attempts=1 must disable retries, got %d attempts", calls)
	}

	// A snapshot swap changes behavior on the already-built handler chain.
	rules.Replace(map[string]Policy{
		"/svc/Method": {Attempts: 4, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
	})
	calls = 0
	if _, err := h(clientCtx("/svc/Method"), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 4 {
		t.Fatalf("updated rule must grant 4 attempts, got %d", calls)
	}

	// Operations without an exact rule keep the fallback.
	calls = 0
	if _, err := h(clientCtx("/svc/Other"), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 1 {
		t.Fatalf("unmatched operation must use fallback, got %d attempts", calls)
	}

	// Outside a transport context the fallback applies; nothing panics.
	calls = 0
	if _, err := h(context.Background(), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 1 {
		t.Fatalf("no-transport call must use fallback, got %d attempts", calls)
	}
}

func TestWithRulesZeroPolicyFallsBackToStatic(t *testing.T) {
	// A table lookup yielding an unusable policy (zero attempts) must fall
	// back to the static policy rather than never invoke the handler.
	rules := governance.NewRules(Policy{})
	var calls int
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls),
		WithRules(rules),
		WithPolicy(Policy{Attempts: 2, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}),
		noSleep(nil))
	if _, err := h(clientCtx("/svc/Method"), nil); err == nil {
		t.Fatal("want failure")
	}
	if calls != 2 {
		t.Fatalf("zero rule must fall back to the static policy, got %d attempts", calls)
	}
}

func TestClientRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct {
		name string
		opts []Option
	}{
		{"nil option", []Option{nil}},
		{"zero attempts", []Option{WithPolicy(Policy{Attempts: 0})}},
		{"negative attempts", []Option{WithPolicy(Policy{Attempts: -1})}},
		{"zero backoff with retries", []Option{WithPolicy(Policy{Attempts: 2})}},
		{"cap below base", []Option{WithPolicy(Policy{Attempts: 2, BaseBackoff: time.Second, MaxBackoff: time.Millisecond})}},
		{"nil rules", []Option{WithRules(nil)}},
		{"nil retryable", []Option{WithRetryable(nil)}},
		{"nil backoff", []Option{WithBackoff(nil)}},
	}
	for _, tc := range cases {
		if _, err := Client(tc.opts...); err == nil {
			t.Errorf("%s: want construction error", tc.name)
		}
	}
}

func TestPolicyValidate(t *testing.T) {
	valid := []Policy{
		{Attempts: 1},
		{Attempts: 1, BaseBackoff: -time.Second}, // backoff unused without retries
		{Attempts: 3, BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("policy %+v must validate, got %v", p, err)
		}
	}
}

func TestRewindRestoresHTTPBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := transport.NewClientContext(context.Background(), &testTransport{request: req})
	// Consume the body as a sent attempt would.
	buf := make([]byte, 16)
	n, _ := req.Body.Read(buf)
	if string(buf[:n]) != "payload" {
		t.Fatalf("setup: want body consumed, got %q", buf[:n])
	}
	if !rewindRequest(ctx) {
		t.Fatal("a request with GetBody must rewind")
	}
	n, _ = req.Body.Read(buf)
	if string(buf[:n]) != "payload" {
		t.Fatalf("want body restored, got %q", buf[:n])
	}
}

func TestRewindRefusesUnreplayableBody(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}
	req.GetBody = nil
	ctx := transport.NewClientContext(context.Background(), &testTransport{request: req})

	var calls int
	cause := notSentErr()
	h := mustClientNext(t, failNTimes(99, cause, &calls), noSleep(nil))
	if _, err := h(ctx, nil); !errors.Is(err, cause) {
		t.Fatalf("want last error, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("an unreplayable body must abandon the retry, got %d attempts", calls)
	}
}

func TestRewindPassesNonHTTPTransport(t *testing.T) {
	if !rewindRequest(context.Background()) {
		t.Fatal("no transport context must not block a retry")
	}
	ctx := transport.NewClientContext(context.Background(), &testTransport{})
	if !rewindRequest(ctx) {
		t.Fatal("a transport without a request must not block a retry")
	}
}

func TestSleepContext(t *testing.T) {
	if err := sleepContext(context.Background(), 0); err != nil {
		t.Fatalf("zero sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepContext(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context must interrupt sleep, got %v", err)
	}
	if err := sleepContext(context.Background(), time.Microsecond); err != nil {
		t.Fatalf("short sleep: %v", err)
	}
}
