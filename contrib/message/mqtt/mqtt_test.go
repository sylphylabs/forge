package mqtt

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/sylphylabs/forge/metadata"
	"github.com/sylphylabs/forge/transport/message"
)

// fakeConn records outbound packets so publish and subscription behaviour can
// be asserted without a broker.
type fakeConn struct {
	mu           sync.Mutex
	published    []*paho.Publish
	subscribed   []*paho.Subscribe
	unsubscribed []*paho.Unsubscribe
	disconnects  int

	publishResp  *paho.PublishResponse
	publishErr   error
	subscribeAck *paho.Suback
	subscribeErr error
	unsubErr     error
	awaitErr     error
}

func (f *fakeConn) Publish(_ context.Context, pub *paho.Publish) (*paho.PublishResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, pub)
	return f.publishResp, f.publishErr
}

func (f *fakeConn) Subscribe(_ context.Context, sub *paho.Subscribe) (*paho.Suback, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subscribed = append(f.subscribed, sub)
	return f.subscribeAck, f.subscribeErr
}

func (f *fakeConn) Unsubscribe(_ context.Context, unsub *paho.Unsubscribe) (*paho.Unsuback, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unsubscribed = append(f.unsubscribed, unsub)
	return nil, f.unsubErr
}

func (f *fakeConn) Disconnect(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnects++
	return nil
}

func (f *fakeConn) AwaitConnection(context.Context) error { return f.awaitErr }

func (f *fakeConn) lastPublish(t *testing.T) *paho.Publish {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.published) == 0 {
		t.Fatal("no publish recorded")
	}
	return f.published[len(f.published)-1]
}

// newTestClient builds a client around the fake connection. It applies the
// options directly rather than going through New, which would try to dial a
// broker.
func newTestClient(t *testing.T, conn connection, opts ...Option) *Client {
	t.Helper()
	cfg := options{subscribeQoS: 1, connectWait: time.Second}
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Client{
		conn:         conn,
		publishQoS:   cfg.publishQoS,
		subscribeQoS: cfg.subscribeQoS,
		retain:       cfg.retain,
		idInUserProp: cfg.idInUserProp,
		errorHandler: cfg.errorHandler,
		connectWait:  cfg.connectWait,
		router:       newRouter(cfg.ackOnError),
	}
}

func TestPublishMapsMessageOntoMQTT5Properties(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn, WithPublishQoS(1))

	msg := message.New([]byte("created"))
	msg.ID = "evt-1"
	msg.Key = "acct-1"
	msg.SetHeader("TraceParent", "00-abc")
	msg.AddHeader("TraceParent", "00-def")
	if err := client.Publish(t.Context(), "accounts/created", msg); err != nil {
		t.Fatal(err)
	}

	pub := conn.lastPublish(t)
	if pub.Topic != "accounts/created" {
		t.Errorf("Topic = %q, want accounts/created", pub.Topic)
	}
	if pub.QoS != 1 {
		t.Errorf("QoS = %d, want 1", pub.QoS)
	}
	if string(pub.Payload) != "created" {
		t.Errorf("Payload = %q, want created", pub.Payload)
	}
	if got := string(pub.Properties.CorrelationData); got != "evt-1" {
		t.Errorf("CorrelationData = %q, want evt-1", got)
	}
	if got := pub.Properties.User.Get(PropertyMessageKey); got != "acct-1" {
		t.Errorf("key property = %q, want acct-1", got)
	}
	if got := pub.Properties.User.GetAll("traceparent"); !reflect.DeepEqual(got, []string{"00-abc", "00-def"}) {
		t.Errorf("traceparent properties = %v, want both values", got)
	}
}

func TestPublishCarriesIDInUserPropertyWhenConfigured(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn, WithIDInUserProperty(true))

	msg := message.New([]byte("body"))
	msg.ID = "evt-2"
	if err := client.Publish(t.Context(), "topic", msg); err != nil {
		t.Fatal(err)
	}

	pub := conn.lastPublish(t)
	if len(pub.Properties.CorrelationData) != 0 {
		t.Errorf("CorrelationData = %q, want empty", pub.Properties.CorrelationData)
	}
	if got := pub.Properties.User.Get(PropertyMessageID); got != "evt-2" {
		t.Errorf("id property = %q, want evt-2", got)
	}
}

func TestPublishRejectsWildcardTopic(t *testing.T) {
	client := newTestClient(t, &fakeConn{})
	for _, topic := range []string{"a/+/c", "a/#"} {
		if err := client.Publish(t.Context(), topic, message.New(nil)); !errors.Is(err, ErrWildcardPublish) {
			t.Errorf("Publish(%q) error = %v, want ErrWildcardPublish", topic, err)
		}
	}
}

func TestPublishReportsRejectionReasonCode(t *testing.T) {
	conn := &fakeConn{publishResp: &paho.PublishResponse{ReasonCode: 0x87}}
	client := newTestClient(t, conn, WithPublishQoS(1))
	if err := client.Publish(t.Context(), "topic", message.New(nil)); !errors.Is(err, ErrPublishRejected) {
		t.Fatalf("Publish error = %v, want ErrPublishRejected", err)
	}
}

func TestPublishValidatesArguments(t *testing.T) {
	client := newTestClient(t, &fakeConn{})
	//nolint:staticcheck // a nil context is exactly what this case asserts.
	if err := client.Publish(nil, "topic", message.New(nil)); !errors.Is(err, ErrNilContext) {
		t.Errorf("nil context error = %v, want ErrNilContext", err)
	}
	if err := client.Publish(t.Context(), "  ", message.New(nil)); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("empty topic error = %v, want ErrEmptyTopic", err)
	}
	if err := client.Publish(t.Context(), "topic", nil); !errors.Is(err, ErrNilMessage) {
		t.Errorf("nil message error = %v, want ErrNilMessage", err)
	}
}

func TestNewRejectsInvalidQoS(t *testing.T) {
	if _, err := New(t.Context(), WithURL("mqtt://127.0.0.1:1883"), WithPublishQoS(3)); !errors.Is(err, ErrInvalidQoS) {
		t.Errorf("publish qos error = %v, want ErrInvalidQoS", err)
	}
	if _, err := New(t.Context(), WithURL("mqtt://127.0.0.1:1883"), WithSubscribeQoS(9)); !errors.Is(err, ErrInvalidQoS) {
		t.Errorf("subscribe qos error = %v, want ErrInvalidQoS", err)
	}
}

func TestNewRequiresURLOrConnection(t *testing.T) {
	if _, err := New(t.Context()); !errors.Is(err, ErrEmptyURL) {
		t.Errorf("error = %v, want ErrEmptyURL", err)
	}
	if _, err := New(t.Context(), WithConnectionManager(nil)); !errors.Is(err, ErrNilConn) {
		t.Errorf("error = %v, want ErrNilConn", err)
	}
	//nolint:staticcheck // a nil context is exactly what this case asserts.
	if _, err := New(nil, WithURL("mqtt://127.0.0.1:1883")); !errors.Is(err, ErrNilContext) {
		t.Errorf("error = %v, want ErrNilContext", err)
	}
}

// The adapter must enable manual acknowledgement, because that is what lets a
// handler error withhold the PUBACK.
func TestClientConfigEnablesManualAcknowledgement(t *testing.T) {
	client := &Client{router: newRouter(false)}
	cfg := options{ackInterval: 7 * time.Millisecond, clientID: "worker-1", sessionExpiry: 300}
	acfg := client.clientConfig(nil, &cfg)

	if !acfg.EnableManualAcknowledgment {
		t.Error("EnableManualAcknowledgment = false, want true")
	}
	if acfg.SendAcksInterval != 7*time.Millisecond {
		t.Errorf("SendAcksInterval = %v, want 7ms", acfg.SendAcksInterval)
	}
	if acfg.ClientID != "worker-1" {
		t.Errorf("ClientID = %q, want worker-1", acfg.ClientID)
	}
	if acfg.SessionExpiryInterval != 300 {
		t.Errorf("SessionExpiryInterval = %d, want 300", acfg.SessionExpiryInterval)
	}
	if len(acfg.OnPublishReceived) != 1 {
		t.Fatalf("OnPublishReceived handlers = %d, want 1", len(acfg.OnPublishReceived))
	}
}

func TestWithClientConfigOverridesDefaults(t *testing.T) {
	client := &Client{router: newRouter(false)}
	cfg := options{configure: func(c *autopaho.ClientConfig) { c.KeepAlive = 99 }}
	if got := client.clientConfig(nil, &cfg).KeepAlive; got != 99 {
		t.Errorf("KeepAlive = %d, want 99", got)
	}
}

func TestSubscribeDeliversConcreteTopicForWildcardFilter(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn)
	type delivery struct {
		topic string
		msg   *message.Message
	}
	got := make(chan delivery, 1)
	sub, err := client.Subscribe(t.Context(), "accounts/+/created", func(ctx context.Context, topic string, msg *message.Message) error {
		md, ok := metadata.FromServerContext(ctx)
		if !ok {
			t.Errorf("server metadata missing from handler context")
		} else if md.Get("traceparent") != "00-abc" {
			t.Errorf("traceparent metadata = %q, want 00-abc", md.Get("traceparent"))
		}
		got <- delivery{topic: topic, msg: msg}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(t.Context())

	client.router.route(paho.PublishReceived{Packet: &paho.Publish{
		Topic:   "accounts/42/created",
		Payload: []byte("created"),
		Properties: &paho.PublishProperties{
			CorrelationData: []byte("evt-9"),
			User: paho.UserProperties{
				{Key: PropertyMessageKey, Value: "acct-9"},
				{Key: "traceparent", Value: "00-abc"},
			},
		},
	}})

	select {
	case d := <-got:
		if d.topic != "accounts/42/created" {
			t.Errorf("topic = %q, want the concrete delivery topic", d.topic)
		}
		if d.msg.ID != "evt-9" {
			t.Errorf("ID = %q, want evt-9", d.msg.ID)
		}
		if d.msg.Key != "acct-9" {
			t.Errorf("Key = %q, want acct-9", d.msg.Key)
		}
		if d.msg.Headers.Get(PropertyMessageKey) != "" {
			t.Errorf("forge-owned property leaked into headers: %v", d.msg.Headers)
		}
		if d.msg.Headers.Get("traceparent") != "00-abc" {
			t.Errorf("traceparent header = %q, want 00-abc", d.msg.Headers.Get("traceparent"))
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}
}

func TestSubscribeReportsRejectedSuback(t *testing.T) {
	conn := &fakeConn{subscribeAck: &paho.Suback{Reasons: []byte{0x87}}}
	client := newTestClient(t, conn)
	if _, err := client.Subscribe(t.Context(), "denied/topic", noopHandler); !errors.Is(err, ErrSubscribeRejected) {
		t.Fatalf("Subscribe error = %v, want ErrSubscribeRejected", err)
	}
	if len(client.router.snapshot()) != 0 {
		t.Error("rejected subscription left a route registered")
	}
}

func TestSubscribeAcceptsDowngradedQoS(t *testing.T) {
	// Reason codes below 0x80 are the granted QoS, not a failure.
	conn := &fakeConn{subscribeAck: &paho.Suback{Reasons: []byte{0x00}}}
	client := newTestClient(t, conn)
	sub, err := client.Subscribe(t.Context(), "topic", noopHandler)
	if err != nil {
		t.Fatalf("Subscribe with downgraded QoS: %v", err)
	}
	defer sub.Close(t.Context())
}

func TestSubscribeValidatesArguments(t *testing.T) {
	client := newTestClient(t, &fakeConn{})
	//nolint:staticcheck // a nil context is exactly what this case asserts.
	if _, err := client.Subscribe(nil, "topic", noopHandler); !errors.Is(err, ErrNilContext) {
		t.Errorf("nil context error = %v, want ErrNilContext", err)
	}
	if _, err := client.Subscribe(t.Context(), " ", noopHandler); !errors.Is(err, ErrEmptyTopic) {
		t.Errorf("empty topic error = %v, want ErrEmptyTopic", err)
	}
	if _, err := client.Subscribe(t.Context(), "topic", nil); !errors.Is(err, ErrNilHandler) {
		t.Errorf("nil handler error = %v, want ErrNilHandler", err)
	}
}

func TestSubscriptionCloseUnsubscribesAndIsIdempotent(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn)
	sub, err := client.Subscribe(t.Context(), "topic", noopHandler)
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(t.Context()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.unsubscribed) != 1 {
		t.Errorf("unsubscribe count = %d, want 1", len(conn.unsubscribed))
	}
}

func TestSubscriptionCancellationStopsDelivery(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn)
	subCtx, cancel := context.WithCancel(t.Context())
	delivered := make(chan struct{}, 1)
	sub, err := client.Subscribe(subCtx, "topic", func(context.Context, string, *message.Message) error {
		delivered <- struct{}{}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := sub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	client.router.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "topic"}})
	select {
	case <-delivered:
		t.Fatal("handler ran after cancellation")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCloseDisconnectsOwnedConnectionOnly(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn)
	if err := client.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if conn.disconnects != 0 {
		t.Errorf("disconnects = %d, want 0 for an application-owned manager", conn.disconnects)
	}
	if err := client.Close(t.Context()); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if err := client.Publish(t.Context(), "topic", message.New(nil)); !errors.Is(err, ErrClosed) {
		t.Errorf("Publish after Close = %v, want ErrClosed", err)
	}

	owned := newTestClient(t, &fakeConn{})
	owned.ownsConn = true
	if err := owned.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owned.conn.(*fakeConn).disconnects != 1 {
		t.Errorf("disconnects = %d, want 1 for an adapter-owned manager", owned.conn.(*fakeConn).disconnects)
	}
}

func TestMessageServerLifecycle(t *testing.T) {
	conn := &fakeConn{}
	client := newTestClient(t, conn)
	server := message.NewServer(client)
	delivered := make(chan string, 1)
	if err := server.Handle("orders/created", func(ctx context.Context, req any) (any, error) {
		topic, _ := message.DestinationFromServerContext(ctx)
		msg, _ := req.(*message.Message)
		delivered <- topic + ":" + string(msg.Body)
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}

	startErr := make(chan error, 1)
	go func() { startErr <- server.Start(t.Context()) }()
	waitForRoutes(t, client, 1)

	client.router.route(paho.PublishReceived{Packet: &paho.Publish{Topic: "orders/created", Payload: []byte("ok")}})
	select {
	case got := <-delivered:
		if got != "orders/created:ok" {
			t.Fatalf("delivery = %q, want orders/created:ok", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delivery")
	}

	if err := server.Stop(t.Context()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-startErr; err != nil {
		t.Fatalf("Start: %v", err)
	}
}

func noopHandler(context.Context, string, *message.Message) error { return nil }

func waitForRoutes(t *testing.T, client *Client, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(client.router.snapshot()) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d routes", want)
}
