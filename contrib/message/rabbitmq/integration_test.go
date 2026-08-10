package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/sylphylabs/forge/transport/message"
)

const (
	// envURL points the tests below at a real RabbitMQ. They are skipped unless
	// it is set, so the default `go test ./...` needs no broker. Run them with:
	//
	//	docker run -d --name rabbit -p 5672:5672 rabbitmq:4
	//	FORGE_RABBITMQ_URL=amqp://guest:guest@127.0.0.1:5672/ go test ./...
	envURL = "FORGE_RABBITMQ_URL"

	// envRestart holds a shell command that restarts that broker.
	envRestart = "FORGE_RABBITMQ_RESTART"
)

func TestIntegrationPublishSubscribeRoundTrip(t *testing.T) {
	client, exchange, queue := newIntegrationClient(t, "roundtrip")

	received := make(chan *message.Message, 1)
	destinations := make(chan string, 1)
	sub, err := client.Subscribe(t.Context(), queue, func(_ context.Context, destination string, msg *message.Message) error {
		destinations <- destination
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = exchange + ".eu"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	if err := client.Publish(t.Context(), queue, msg); err != nil {
		t.Fatal(err)
	}

	if got := waitForString(t, destinations); got != exchange+".eu" {
		t.Errorf("destination = %q, want the concrete routing key", got)
	}
	got := waitForMessage(t, received)
	if got.ID != "evt-1" {
		t.Errorf("ID = %q, want evt-1", got.ID)
	}
	if got.Key != exchange+".eu" {
		t.Errorf("Key = %q, want the published key", got.Key)
	}
	if string(got.Body) != "created" {
		t.Errorf("Body = %q, want created", got.Body)
	}
	if values := got.Headers.Values("traceparent"); len(values) != 2 || values[0] != "00-abc" || values[1] != "00-def" {
		t.Errorf("traceparent values = %v, want both values", values)
	}
}

func TestIntegrationHandlerErrorDropsWithoutRequeue(t *testing.T) {
	client, _, queue := newIntegrationClient(t, "drop")

	attempts := make(chan struct{}, 4)
	sub, err := client.Subscribe(t.Context(), queue, func(context.Context, string, *message.Message) error {
		attempts <- struct{}{}
		return errors.New("permanent")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	if err := client.Publish(t.Context(), queue, message.New([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempts:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the first delivery")
	}
	// A dropped message must not come back. Waiting for a redelivery that
	// should never arrive is the only way to observe requeue=false.
	select {
	case <-attempts:
		t.Fatal("message was redelivered, want a drop without requeue")
	case <-time.After(2 * time.Second):
	}
}

func TestIntegrationHandlerErrorRequeuesWhenClassified(t *testing.T) {
	client, _, queue := newIntegrationClient(t, "requeue", WithErrorClassifier(
		func(context.Context, *message.Message, error) Disposition { return Requeue },
	))

	attempts := make(chan struct{}, 8)
	sub, err := client.Subscribe(t.Context(), queue, func(context.Context, string, *message.Message) error {
		select {
		case attempts <- struct{}{}:
		default:
		}
		return errors.New("transient")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	if err := client.Publish(t.Context(), queue, message.New([]byte("x"))); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		select {
		case <-attempts:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for delivery %d, want a redelivery after requeue", i+1)
		}
	}
}

func TestIntegrationMandatoryPublishReportsUnroutable(t *testing.T) {
	url := integrationURL(t)
	name := uniqueName("unrouted")
	client, err := New(
		WithURL(url),
		WithBindings(map[string]Binding{
			// The queue binds only "routed.#", so a message published under a
			// different key reaches a real exchange that has nowhere to put it.
			name: {
				Queue:    Queue{Name: name, Durable: true, AutoDelete: true, BindingKeys: []string{"routed.#"}},
				Exchange: Exchange{Name: name, Kind: amqp.ExchangeTopic, AutoDelete: true},
			},
		}),
		WithDeclare(true),
		WithReturnTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	// Topology is declared on the consuming path, so the exchange only exists
	// once a subscription has been created.
	sub, err := client.Subscribe(t.Context(), name, func(context.Context, string, *message.Message) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	msg := message.New([]byte("x"))
	msg.Key = "unrouted.key"
	if err := client.Publish(t.Context(), name, msg); !errors.Is(err, ErrPublishReturned) {
		t.Fatalf("Publish error = %v, want ErrPublishReturned", err)
	}
}

// TestIntegrationRecoversAcrossBrokerRestart needs to take the broker away and
// bring it back, which only the operator of the container can do. It is gated
// on its own variable so the rest of the integration suite still runs against a
// broker the test cannot restart.
//
//	FORGE_RABBITMQ_RESTART="docker restart my-rabbitmq" go test ./...
func TestIntegrationRecoversAcrossBrokerRestart(t *testing.T) {
	restart := os.Getenv(envRestart)
	if restart == "" {
		t.Skipf("set %s to a command that restarts the broker", envRestart)
	}
	client, _, queue := newDurableIntegrationClient(t, "recovery")

	received := make(chan string, 4)
	sub, err := client.Subscribe(t.Context(), queue, func(_ context.Context, _ string, msg *message.Message) error {
		received <- string(msg.Body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer closeSubscription(t, sub)

	if err := client.Publish(t.Context(), queue, message.New([]byte("before"))); err != nil {
		t.Fatal(err)
	}
	if got := waitForString(t, received); got != "before" {
		t.Fatalf("body = %q, want before", got)
	}

	command := exec.Command("sh", "-c", restart)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("restart broker: %v: %s", err, output)
	}

	// Publishing is the readiness probe: it fails until the adapter has
	// redialed, and succeeding means the broker is back.
	deadline := time.Now().Add(90 * time.Second)
	for {
		err := client.Publish(t.Context(), queue, message.New([]byte("after")))
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publisher never recovered: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if got := waitForString(t, received); got != "after" {
		t.Errorf("body = %q, want after; the consumer did not recover", got)
	}
}

func TestIntegrationMessageServerLifecycle(t *testing.T) {
	client, _, queue := newIntegrationClient(t, "server")

	delivered := make(chan string, 1)
	server := message.NewServer(client)
	if err := server.Handle(queue, func(ctx context.Context, req any) (any, error) {
		destination, _ := message.DestinationFromServerContext(ctx)
		msg, _ := req.(*message.Message)
		delivered <- destination + ":" + string(msg.Body)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(t.Context()) }()

	// message.Server has no readiness signal, so publish until the
	// subscription exists rather than sleeping for a fixed interval.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := client.Publish(t.Context(), queue, message.New([]byte("ok"))); err == nil {
			break
		} else if time.Now().After(deadline) {
			t.Fatalf("publish never became routable: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}

	select {
	case got := <-delivered:
		if got != queue+":ok" {
			t.Errorf("delivery = %q, want %s:ok", got, queue)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for delivery")
	}

	if err := server.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("Start: %v", err)
	}
}

// newIntegrationClient builds a client bound to a freshly declared, auto-delete
// topic exchange and queue, so parallel runs and reruns never share state.
func newIntegrationClient(t *testing.T, name string, opts ...Option) (client *Client, exchange, queue string) {
	t.Helper()
	return newTestClient(t, name, true, opts...)
}

// newDurableIntegrationClient keeps its topology across a broker restart.
// Auto-delete topology would be gone when the broker came back, so a recovery
// test could not tell recovery apart from a vanished queue.
func newDurableIntegrationClient(t *testing.T, name string, opts ...Option) (client *Client, exchange, queue string) {
	t.Helper()
	return newTestClient(t, name, false, opts...)
}

// newTestClient declares one exchange and queue for a single test. Queues are
// always durable because RabbitMQ 4 refuses transient non-exclusive queues.
func newTestClient(t *testing.T, name string, autoDelete bool, opts ...Option) (client *Client, exchange, queue string) {
	t.Helper()
	url := integrationURL(t)
	exchange = uniqueName(name)
	queue = exchange
	options := append([]Option{
		WithURL(url),
		WithBindings(map[string]Binding{
			queue: {
				Queue:    Queue{Name: queue, Durable: true, AutoDelete: autoDelete, BindingKeys: []string{"#"}},
				Exchange: Exchange{Name: exchange, Kind: amqp.ExchangeTopic, Durable: !autoDelete, AutoDelete: autoDelete},
			},
		}),
		WithDeclare(true),
		WithPrefetch(1),
	}, opts...)

	client, err := New(options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !autoDelete {
			deleteTopology(t, url, exchange, queue)
		}
		if err := client.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return client, exchange, queue
}

// deleteTopology removes durable topology a test declared, which the broker
// would otherwise keep after the test process exits.
func deleteTopology(t *testing.T, url, exchange, queue string) {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Logf("cleanup dial: %v", err)
		return
	}
	defer conn.Close()
	channel, err := conn.Channel()
	if err != nil {
		t.Logf("cleanup channel: %v", err)
		return
	}
	if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
		t.Logf("cleanup queue %q: %v", queue, err)
		return
	}
	if err := channel.ExchangeDelete(exchange, false, false); err != nil {
		t.Logf("cleanup exchange %q: %v", exchange, err)
	}
}

func integrationURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv(envURL)
	if url == "" {
		t.Skipf("set %s to run RabbitMQ integration tests", envURL)
	}
	return url
}

func uniqueName(name string) string {
	return fmt.Sprintf("forge-test-%s-%d", name, time.Now().UnixNano())
}

func waitForMessage(t *testing.T, messages <-chan *message.Message) *message.Message {
	t.Helper()
	select {
	case msg := <-messages:
		return msg
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
		return nil
	}
}

func waitForString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for a delivery")
		return ""
	}
}
