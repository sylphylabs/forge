package rabbitmq_test

// End-to-end tests for the message transport, against a real RabbitMQ.
//
// Every other test of this chain stops at a seam. The unit tests in this
// package drive an in-process fake broker; middleware_parity_test.go in
// transport/message calls middleware directly with a hand-built envelope; the
// generator's own tests assert on generated source rather than on a running
// consumer. Each is a useful test and none of them settles a delivery.
//
// These do. They run the whole chain the generator produces — a subscribe
// annotation, the generated registration, a real broker, the middleware chain,
// and a handler reading its destination from context — and assert on what the
// broker actually did with the message.
//
// Why this file lives here rather than in internal/e2e: internal/e2e is in the
// root module, and the root module has no AMQP dependency. That is deliberate —
// contrib/message/rabbitmq/README.md states the adapter "does not add RabbitMQ
// to the root Forge module", and the root go.mod has no amqp091 entry. Putting
// these tests in internal/e2e would put a broker driver in the dependency graph
// of every Forge consumer, which is the one property the split module exists to
// protect. The tests live in the module that already owns the dependency, and
// borrow internal/e2e's gating and container conventions instead of its
// location.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"google.golang.org/protobuf/proto"

	"github.com/sylphylabs/forge/contrib/message/rabbitmq"
	forgeerrors "github.com/sylphylabs/forge/errors"
	"github.com/sylphylabs/forge/internal/testdata/orderevents"
	"github.com/sylphylabs/forge/middleware/recovery"
	"github.com/sylphylabs/forge/middleware/timeout"
	"github.com/sylphylabs/forge/transport/message"
)

const (
	// envE2E opts in to these tests and lets them manage their own broker
	// container, mirroring FORGE_E2E in internal/e2e. It is separate from
	// FORGE_RABBITMQ_URL, which points the integration tests at a broker
	// somebody else started.
	envE2E = "FORGE_MESSAGE_E2E"

	// envE2EURL reuses an already-running broker instead of starting one. A
	// developer with a container up avoids a start/stop cycle per run.
	envE2EURL = "FORGE_MESSAGE_E2E_URL"

	// e2eImage is pinned to the same major version the integration tests
	// document, so a passing run here means the same broker family.
	e2eImage = "rabbitmq:4"

	// e2eContainer is fixed so a crashed run leaves a container this suite can
	// identify and remove on its next start rather than leaking one per run.
	e2eContainer = "forge-message-e2e"

	// e2ePort is deliberately not 5672 or 15672: a developer's own broker may
	// hold either, and a port collision would look like a broker failure.
	e2ePort = "25673"
)

// brokerURL is the AMQP URL every test in this file dials, set once by TestMain.
var brokerURL string

// TestMain starts one broker for the whole file. A container per test would
// dominate the runtime and prove nothing extra: the tests isolate themselves
// through unique topology names, not through separate brokers.
func TestMain(m *testing.M) {
	if os.Getenv(envE2E) == "" {
		// Each test states its own skip, so a reader of the output sees why
		// nothing ran rather than an empty pass.
		os.Exit(m.Run())
	}

	if url := os.Getenv(envE2EURL); url != "" {
		brokerURL = url
		os.Exit(m.Run())
	}

	url, stop, err := startBroker()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start broker: %v\n", err)
		os.Exit(1)
	}
	brokerURL = url
	code := m.Run()
	stop()
	os.Exit(code)
}

// startBroker runs a RabbitMQ container and waits until it accepts AMQP
// connections. Readiness is probed by dialling rather than by sleeping: the
// container reports "running" long before the broker listens.
func startBroker() (url string, stop func(), err error) {
	// A container left by a crashed run would make `docker run` fail on the
	// name. Removing it first makes the suite rerunnable without manual
	// cleanup.
	_ = exec.Command("docker", "rm", "-f", e2eContainer).Run()

	run := exec.Command("docker", "run", "--detach",
		"--name", e2eContainer,
		"--publish", e2ePort+":5672",
		e2eImage)
	if output, err := run.CombinedOutput(); err != nil {
		return "", nil, fmt.Errorf("docker run: %w: %s", err, output)
	}
	stop = func() { _ = exec.Command("docker", "rm", "-f", e2eContainer).Run() }

	url = "amqp://guest:guest@127.0.0.1:" + e2ePort + "/"
	deadline := time.Now().Add(90 * time.Second)
	for {
		conn, dialErr := amqp.Dial(url)
		if dialErr == nil {
			_ = conn.Close()
			return url, stop, nil
		}
		if time.Now().After(deadline) {
			stop()
			return "", nil, fmt.Errorf("broker never became ready: %w", dialErr)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// requireBroker skips unless this suite was opted in to.
//
// Without the variable a plain `go test ./...` must pass on a machine with no
// Docker, which is why the default is to skip rather than to try and fail.
func requireBroker(t *testing.T) {
	t.Helper()
	if os.Getenv(envE2E) == "" {
		t.Skipf("set %s=1 to run the message end-to-end tests (needs Docker)", envE2E)
	}
}

// TestPanicInOneHandlerLeavesTheWorkerConsuming is the defect ADR-0014 claims to
// have fixed, reproduced against a real broker.
//
// Before the handler signature converged, a message consumer had one middleware
// available, so a panic on a malformed payload unwound the delivery goroutine
// and the worker stopped consuming. Everything behind it in the queue went
// undelivered.
//
// The assertion is deliberately not "recovery returned an error". It is that a
// message published *after* the panicking one is delivered: that is only
// possible if the goroutine that ran the panicking handler survived and came
// back for the next delivery. A worker killed by the panic would leave the
// second message sitting in the queue and this test would time out.
func TestPanicInOneHandlerLeavesTheWorkerConsuming(t *testing.T) {
	requireBroker(t)

	const poison, healthy = "poison", "healthy"

	delivered := make(chan string, 4)
	var panics atomic.Int64

	srv := &recordingOrderEvents{
		onCreated: func(_ context.Context, in *orderevents.OrderCreated) error {
			if in.GetId() == poison {
				panics.Add(1)
				panic("malformed payload: " + in.GetId())
			}
			delivered <- in.GetId()
			return nil
		},
	}

	// recovery is the only middleware here, so a survival is attributable to it
	// and to nothing else in a longer chain.
	h := newHarness(t, "panic", srv, harnessOptions{
		serverOptions: []message.ServerOption{
			message.WithMiddleware(recovery.Recovery()),
		},
	})

	// The poison message goes first and must be settled before the next one is
	// delivered; prefetch is 1, so the broker cannot hand over the second until
	// the first is acknowledged. That ordering is what makes the second
	// delivery evidence of survival rather than of parallelism.
	h.publishCreated(t, orderevents.DestinationOrderEventsOnOrderCreated, &orderevents.OrderCreated{Id: poison, Customer: "acme"})
	h.publishCreated(t, orderevents.DestinationOrderEventsOnOrderCreated, &orderevents.OrderCreated{Id: healthy, Customer: "acme"})

	// The worker is alive if and only if the message queued behind the panic
	// arrives.
	if got := receive(t, delivered, 20*time.Second); got != healthy {
		t.Errorf("delivery after the panic = %q, want %q", got, healthy)
	}
	if n := panics.Load(); n < 1 {
		t.Errorf("the poison handler panicked %d times, want at least once; the test proved nothing", n)
	}

	// recovery must have turned the panic into an error the adapter could
	// settle, rather than letting it escape as a crash.
	failure := h.waitFailure(t, 10*time.Second)
	if failure.Stage != rabbitmq.StageHandler {
		t.Errorf("failure stage = %q, want %q", failure.Stage, rabbitmq.StageHandler)
	}
	if !errors.Is(failure.Err, recovery.ErrUnknownRequest) {
		t.Errorf("failure error = %v, want the recovery sentinel", failure.Err)
	}
}

// TestGeneratedRegistrationDeliversADecodedMessage runs the generator's output
// against a real broker: the subscribe annotation's destination, the generated
// RegisterOrderEventsMessageServer, the proto.Unmarshal inside the generated
// handler, and the middleware.UnaryHandler adaptation.
func TestGeneratedRegistrationDeliversADecodedMessage(t *testing.T) {
	requireBroker(t)

	type received struct {
		id, customer, destination string
	}
	got := make(chan received, 1)

	srv := &recordingOrderEvents{
		onCreated: func(ctx context.Context, in *orderevents.OrderCreated) error {
			// The destination is read from context, the way an HTTP handler
			// reads its operation, rather than passed as a parameter.
			destination, ok := message.DestinationFromServerContext(ctx)
			if !ok {
				t.Error("no destination in the handler context")
			}
			got <- received{id: in.GetId(), customer: in.GetCustomer(), destination: destination}
			return nil
		},
	}
	h := newHarness(t, "generated", srv, harnessOptions{})

	h.publishCreated(t, orderevents.DestinationOrderEventsOnOrderCreated,
		&orderevents.OrderCreated{Id: "order-1", Customer: "acme"})

	delivery := receiveValue(t, got, 20*time.Second)
	if delivery.id != "order-1" || delivery.customer != "acme" {
		t.Errorf("decoded message = %+v, want id=order-1 customer=acme", delivery)
	}
	if delivery.destination != orderevents.DestinationOrderEventsOnOrderCreated {
		t.Errorf("destination = %q, want %q", delivery.destination, orderevents.DestinationOrderEventsOnOrderCreated)
	}
}

// TestDestinationPrefixOverrideBindsTheOverriddenTopic asserts that the
// registration option changes the topic actually bound at the broker, not just
// a value in the generated code.
//
// The proof is that the handler receives the prefixed routing key. A binding
// that had kept the declared destination would not match a message published
// under the prefixed one, and the mandatory publish would fail as unroutable.
func TestDestinationPrefixOverrideBindsTheOverriddenTopic(t *testing.T) {
	requireBroker(t)

	const prefix = "eu."
	destinations := make(chan string, 1)

	srv := &recordingOrderEvents{
		onCreated: func(ctx context.Context, _ *orderevents.OrderCreated) error {
			destination, _ := message.DestinationFromServerContext(ctx)
			destinations <- destination
			return nil
		},
	}
	h := newHarness(t, "prefix", srv, harnessOptions{
		registerOptions: []orderevents.OrderEventsMessageRegisterOption{
			orderevents.WithOrderEventsMessageDestinationPrefix(prefix),
		},
		destinationPrefix: prefix,
	})

	want := prefix + orderevents.DestinationOrderEventsOnOrderCreated
	h.publishCreated(t, want, &orderevents.OrderCreated{Id: "order-2", Customer: "acme"})

	if got := receive(t, destinations, 20*time.Second); got != want {
		t.Errorf("destination = %q, want the prefixed topic %q", got, want)
	}
}

// TestWildcardBindingReportsTheConcreteDestination is the core claim ADR-0014
// makes about Transport.Operation(): under a wildcard subscription a handler
// sees the destination that actually delivered the message, not the pattern
// that matched it.
//
// A queue bound with "order.#" receives "order.created.eu". Reporting the
// pattern would make every routing-key-dependent decision in a handler — and
// every middleware matcher — see one opaque value for a whole family of
// messages. This is worth asserting on a real broker because the concrete key
// comes from the broker's own delivery frame, so a fake could assert whatever
// the adapter passed it.
func TestWildcardBindingReportsTheConcreteDestination(t *testing.T) {
	requireBroker(t)

	const (
		pattern  = "order.#"
		concrete = "order.created.eu"
	)
	destinations := make(chan string, 1)

	srv := &recordingOrderEvents{
		onCreated: func(ctx context.Context, _ *orderevents.OrderCreated) error {
			destination, _ := message.DestinationFromServerContext(ctx)
			destinations <- destination
			return nil
		},
	}
	h := newHarness(t, "wildcard", srv, harnessOptions{bindingKeys: []string{pattern}})

	// Publishing under the concrete key requires overriding Message.Key, since
	// the routing key is what a topic exchange matches against the pattern.
	h.publishCreatedWithKey(t, orderevents.DestinationOrderEventsOnOrderCreated, concrete,
		&orderevents.OrderCreated{Id: "order-3", Customer: "acme"})

	got := receive(t, destinations, 20*time.Second)
	if got == pattern {
		t.Fatalf("destination = %q, want the concrete key %q, not the pattern", got, concrete)
	}
	if got != concrete {
		t.Errorf("destination = %q, want %q", got, concrete)
	}
}

// TestTimeoutMiddlewareAppliesOnTheMessagePath verifies a second, non-recovery
// middleware on the delivery path, which is the general benefit the converged
// signature was for: middleware written for HTTP and gRPC works on a consumer
// unmodified.
//
// timeout is chosen because its effect is observable from the handler alone —
// the handler's own context is cancelled — so the assertion does not depend on
// inspecting a log or a metric.
func TestTimeoutMiddlewareAppliesOnTheMessagePath(t *testing.T) {
	requireBroker(t)

	type outcome struct {
		hadDeadline bool
		err         error
	}
	outcomes := make(chan outcome, 1)

	srv := &recordingOrderEvents{
		onCreated: func(ctx context.Context, _ *orderevents.OrderCreated) error {
			_, hadDeadline := ctx.Deadline()
			// Outlive the deadline. A handler that ignored its context would
			// return nil here and the delivery would be acked.
			<-ctx.Done()
			outcomes <- outcome{hadDeadline: hadDeadline, err: ctx.Err()}
			return ctx.Err()
		},
	}
	h := newHarness(t, "timeout", srv, harnessOptions{
		serverOptions: []message.ServerOption{
			message.WithMiddleware(timeout.Server(timeout.WithTimeout(200 * time.Millisecond))),
		},
	})

	h.publishCreated(t, orderevents.DestinationOrderEventsOnOrderCreated,
		&orderevents.OrderCreated{Id: "order-4", Customer: "acme"})

	result := receiveValue(t, outcomes, 20*time.Second)
	if !result.hadDeadline {
		t.Error("the handler context carried no deadline; the timeout middleware did not run")
	}
	if !errors.Is(result.err, context.DeadlineExceeded) {
		t.Errorf("handler context error = %v, want DeadlineExceeded", result.err)
	}

	// The expired handler's error must reach the adapter, which is what lets it
	// settle the delivery rather than leaking a prefetch slot.
	//
	// The middleware deliberately remaps context.DeadlineExceeded to its own
	// ErrTimeout, so that a deadline this middleware imposed is distinguishable
	// from one the caller inherited. The assertion matches that sentinel.
	failure := h.waitFailure(t, 10*time.Second)
	if failure.Stage != rabbitmq.StageHandler {
		t.Errorf("failure stage = %q, want %q", failure.Stage, rabbitmq.StageHandler)
	}
	if !forgeerrors.Is(failure.Err, timeout.ErrTimeout) {
		t.Errorf("reported error = %v, want timeout.ErrTimeout", failure.Err)
	}
}

// TestErrorClassifierRequeuesAFailedDelivery checks the mechanism that replaced
// a dedicated Nacker: a handler error plus a classifier returning Requeue puts
// the message back on the queue.
//
// The assertion is a second delivery of the same message, which only the broker
// can produce.
func TestErrorClassifierRequeuesAFailedDelivery(t *testing.T) {
	requireBroker(t)

	attempts := make(chan string, 8)
	var calls atomic.Int64

	srv := &recordingOrderEvents{
		onCreated: func(_ context.Context, in *orderevents.OrderCreated) error {
			// Non-blocking so a fast requeue loop cannot deadlock the handler
			// on a full channel and stall the subscription's shutdown.
			select {
			case attempts <- in.GetId():
			default:
			}
			// Succeed on the third attempt so the requeue loop terminates and
			// the message is not left cycling after the test returns.
			if calls.Add(1) >= 3 {
				return nil
			}
			return errors.New("transient failure")
		},
	}
	h := newHarness(t, "requeue", srv, harnessOptions{
		clientOptions: []rabbitmq.Option{
			rabbitmq.WithErrorClassifier(func(context.Context, *message.Message, error) rabbitmq.Disposition {
				return rabbitmq.Requeue
			}),
		},
	})

	h.publishCreated(t, orderevents.DestinationOrderEventsOnOrderCreated,
		&orderevents.OrderCreated{Id: "order-5", Customer: "acme"})

	// Two deliveries of one published message is the redelivery: the second can
	// only come from the broker having requeued it.
	for i := range 2 {
		if got := receive(t, attempts, 20*time.Second); got != "order-5" {
			t.Fatalf("delivery %d = %q, want order-5", i+1, got)
		}
	}
}

// recordingOrderEvents implements the generated server interface with
// per-test behaviour. Using the generated interface rather than a hand-written
// handler is the point: it is the type the generator asks an application to
// satisfy.
type recordingOrderEvents struct {
	onCreated func(context.Context, *orderevents.OrderCreated) error
	onShipped func(context.Context, *orderevents.OrderShipped) error
}

func (s *recordingOrderEvents) OnOrderCreated(ctx context.Context, in *orderevents.OrderCreated) error {
	if s.onCreated == nil {
		return nil
	}
	return s.onCreated(ctx, in)
}

func (s *recordingOrderEvents) OnOrderShipped(ctx context.Context, in *orderevents.OrderShipped) error {
	if s.onShipped == nil {
		return nil
	}
	return s.onShipped(ctx, in)
}

type harnessOptions struct {
	serverOptions   []message.ServerOption
	clientOptions   []rabbitmq.Option
	registerOptions []orderevents.OrderEventsMessageRegisterOption

	// bindingKeys overrides the queue's binding keys. Empty binds each declared
	// destination by its exact name.
	bindingKeys []string

	// destinationPrefix mirrors a registration prefix override into the
	// adapter's binding map, which is keyed by the resolved destination.
	destinationPrefix string
}

// harness owns one test's client, server, and topology. It exists because every
// test needs the same six-step setup — unique names, a bound client, a
// registered generated server, a started server, a readiness wait, and ordered
// teardown — and because getting the teardown order wrong leaks a queue.
type harness struct {
	client   *rabbitmq.Client
	server   *message.Server
	exchange string
	queue    string
	failures chan rabbitmq.Failure

	// probeDestination and probeKey address a queue nothing consumes, used only
	// to observe that the topology is live. See waitReady.
	probeDestination string
	probeKey         string
}

func newHarness(t *testing.T, name string, srv orderevents.OrderEventsMessageServer, opts harnessOptions) *harness {
	t.Helper()

	// Unique per run so a rerun, or a leftover queue from a failed run, cannot
	// feed this test messages it did not publish.
	base := fmt.Sprintf("forge-e2e-%s-%d", name, time.Now().UnixNano())
	h := &harness{
		exchange: base,
		queue:    base,
		failures: make(chan rabbitmq.Failure, 16),
		// "forge-e2e-probe" is a single token that no test's binding key can
		// match: the tests bind either an exact "order.*" destination or the
		// "order.#" pattern, and neither matches it.
		probeDestination: base + "-probe",
		probeKey:         "forge-e2e-probe",
	}

	// The destinations the generated registration will resolve to, which are
	// also the keys the adapter's binding map must use.
	destinations := []string{
		opts.destinationPrefix + orderevents.DestinationOrderEventsOnOrderCreated,
		opts.destinationPrefix + orderevents.DestinationOrderEventsOnOrderShipped,
	}

	// Each destination gets its own queue.
	//
	// Sharing one queue across destinations would put two consumers on it, and
	// RabbitMQ round-robins between consumers on the same queue rather than
	// routing by binding key. An OrderCreated message would then be handed to
	// whichever consumer came next — half the time the OnOrderShipped one,
	// which would fail to decode it. A queue per destination is also what a
	// real deployment does, since each is a separate work stream.
	bindings := make(map[string]rabbitmq.Binding, len(destinations)+1)
	for i, destination := range destinations {
		// Bind the exact destination so a publish to it is routable, which the
		// default mandatory publish requires.
		keys := []string{destination}
		// A wildcard override applies only to the first destination. Applying it
		// to both would make one message match two queues, and the
		// OnOrderShipped handler would be handed an OrderCreated body.
		if len(opts.bindingKeys) > 0 && i == 0 {
			keys = opts.bindingKeys
		}
		bindings[destination] = rabbitmq.Binding{
			Queue: rabbitmq.Queue{
				Name: fmt.Sprintf("%s-%d", h.queue, i),
				// RabbitMQ 4 refuses transient non-exclusive queues, so the
				// queue is durable and auto-deleted instead.
				Durable:     true,
				AutoDelete:  true,
				BindingKeys: keys,
			},
			Exchange: rabbitmq.Exchange{
				Name:       h.exchange,
				Kind:       amqp.ExchangeTopic,
				AutoDelete: true,
			},
		}
	}

	// The probe's own queue on the same exchange. It is declared alongside the
	// others, so once a publish to it is routable the exchange exists and the
	// test's queue is bound too — both were declared by the same Subscribe.
	bindings[h.probeDestination] = rabbitmq.Binding{
		Queue: rabbitmq.Queue{
			Name:        h.probeDestination,
			Durable:     true,
			AutoDelete:  true,
			BindingKeys: []string{h.probeKey},
		},
		Exchange: rabbitmq.Exchange{
			Name:       h.exchange,
			Kind:       amqp.ExchangeTopic,
			AutoDelete: true,
		},
	}

	clientOptions := append([]rabbitmq.Option{
		rabbitmq.WithURL(brokerURL),
		rabbitmq.WithBindings(bindings),
		rabbitmq.WithDeclare(true),
		// Prefetch 1 makes delivery order observable: the broker cannot hand
		// over the next message until the current one is settled. The panic
		// test depends on that.
		rabbitmq.WithPrefetch(1),
		rabbitmq.WithErrorHandler(func(_ context.Context, failure rabbitmq.Failure) {
			select {
			case h.failures <- failure:
			default:
			}
		}),
	}, opts.clientOptions...)

	client, err := rabbitmq.New(clientOptions...)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	h.client = client

	h.server = message.NewServer(client, opts.serverOptions...)
	if err := orderevents.RegisterOrderEventsMessageServer(h.server, srv, opts.registerOptions...); err != nil {
		t.Fatalf("register generated server: %v", err)
	}
	// The probe needs a binding of its own for the adapter to declare its queue,
	// and its handler must drain deliveries so they do not hold the prefetch
	// slot. It is registered last, so the generated bindings — the ones under
	// test — are subscribed first.
	if err := h.server.Handle(h.probeDestination, func(context.Context, any) (any, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("register readiness probe: %v", err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- h.server.Start(context.WithoutCancel(t.Context())) }()

	// Teardown is registered before the readiness wait so a broker that never
	// becomes ready still gets its server stopped and client closed.
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 15*time.Second)
		defer cancel()
		if err := h.server.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
		select {
		case err := <-startErr:
			if err != nil {
				t.Errorf("Start: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("Start did not return after Stop")
		}
		if err := h.client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	h.waitReady(t)
	return h
}

// waitReady blocks until the subscription exists.
//
// message.Server has no readiness signal, and the adapter declares topology on
// the consuming path, so a publish before Subscribe has run fails as
// unroutable. A mandatory publish that succeeds is therefore the readiness
// probe: it proves the exchange exists and a queue is bound to it. Sleeping a
// fixed interval instead would be flaky on a loaded machine.
//
// The probe must not reach a handler. An earlier version published to the
// destination under test, and the probe's own empty message satisfied the
// assertion before the real one arrived — every test passed on a payload it
// never sent. The probe therefore goes to a queue bound only to itself, so
// readiness is observed without putting a message where a test is looking.
func (h *harness) waitReady(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		msg := message.New([]byte("ready"))
		msg.Key = h.probeKey
		err := h.client.Publish(t.Context(), h.probeDestination, msg)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("subscription never became ready: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// publishCreated publishes a protobuf-encoded OrderCreated to destination.
func (h *harness) publishCreated(t *testing.T, destination string, in *orderevents.OrderCreated) {
	t.Helper()
	h.publishCreatedWithKey(t, destination, destination, in)
}

// publishCreatedWithKey publishes under an explicit routing key, which a
// wildcard binding needs in order to receive a concrete destination.
func (h *harness) publishCreatedWithKey(t *testing.T, destination, key string, in *orderevents.OrderCreated) {
	t.Helper()
	body, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg := message.New(body)
	msg.ID = in.GetId()
	msg.Key = key
	if err := h.client.Publish(t.Context(), destination, msg); err != nil {
		t.Fatalf("publish to %q with key %q: %v", destination, key, err)
	}
}

// waitFailure returns the next failure the adapter reported, ignoring ones the
// readiness probe or an unrelated stage produced.
func (h *harness) waitFailure(t *testing.T, within time.Duration) rabbitmq.Failure {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case failure := <-h.failures:
			// Consume failures are recovery noise from teardown, not the
			// handler outcome a test is asserting on.
			if failure.Stage == rabbitmq.StageConsume {
				continue
			}
			return failure
		case <-deadline:
			t.Fatal("timed out waiting for the adapter to report a failure")
			return rabbitmq.Failure{}
		}
	}
}

func receive(t *testing.T, values <-chan string, within time.Duration) string {
	t.Helper()
	return receiveValue(t, values, within)
}

// receiveValue bounds every wait in this file, so a broken chain fails with a
// message instead of hanging until the package timeout.
func receiveValue[T any](t *testing.T, values <-chan T, within time.Duration) T {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(within):
		var zero T
		t.Fatalf("timed out after %s waiting for a delivery", within)
		return zero
	}
}
