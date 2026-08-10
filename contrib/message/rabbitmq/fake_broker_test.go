package rabbitmq

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// The fakes below stand in for a broker so delivery, acknowledgement, topology,
// and recovery behavior can be asserted deterministically. Integration tests in
// integration_test.go cover the same paths against a real RabbitMQ.

type publishedMessage struct {
	exchange  string
	key       string
	mandatory bool
	msg       amqp.Publishing
}

type settlement struct {
	acked    bool
	nacked   bool
	requeue  bool
	multiple bool
}

// fakeAcknowledger records the single settlement the adapter makes for one
// delivery. amqp091.Delivery calls it through the Acknowledger interface.
type fakeAcknowledger struct {
	settled chan settlement
}

func (a *fakeAcknowledger) Ack(_ uint64, multiple bool) error {
	a.settle(settlement{acked: true, multiple: multiple})
	return nil
}

func (a *fakeAcknowledger) Nack(_ uint64, multiple, requeue bool) error {
	a.settle(settlement{nacked: true, multiple: multiple, requeue: requeue})
	return nil
}

func (a *fakeAcknowledger) Reject(_ uint64, requeue bool) error {
	a.settle(settlement{nacked: true, requeue: requeue})
	return nil
}

func (a *fakeAcknowledger) settle(s settlement) {
	select {
	case a.settled <- s:
	default:
	}
}

func (a *fakeAcknowledger) wait(t *testing.T) settlement {
	t.Helper()
	select {
	case s := <-a.settled:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the delivery to be settled")
		return settlement{}
	}
}

type fakeChannel struct {
	conn *fakeConn

	mu                sync.Mutex
	published         []publishedMessage
	declaredExchanges []string
	declaredQueues    []string
	boundQueues       []string
	deliveries        chan amqp.Delivery
	notifyClose       []chan *amqp.Error
	notifyReturn      []chan amqp.Return
	consuming         bool

	prefetch    int
	qosGlobal   bool
	autoAck     bool
	consumerTag string
	closed      atomic.Bool
}

func (c *fakeChannel) Publish(_ context.Context, exchange, key string, mandatory bool, msg amqp.Publishing) (Confirmation, error) {
	if c.closed.Load() {
		return nil, amqp.ErrClosed
	}
	if err := c.conn.publishError(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.published = append(c.published, publishedMessage{exchange: exchange, key: key, mandatory: mandatory, msg: msg})
	returns := append([]chan amqp.Return(nil), c.notifyReturn...)
	c.mu.Unlock()

	if c.conn.returnOnPublish {
		for _, ch := range returns {
			select {
			case ch <- amqp.Return{ReplyCode: amqp.NoRoute, ReplyText: "NO_ROUTE", Exchange: exchange, RoutingKey: key}:
			default:
			}
		}
	}
	return settledConfirmation(c.conn.confirmAcked), nil
}

func (c *fakeChannel) Consume(_, consumer string, autoAck, _, _, _ bool, _ amqp.Table) (<-chan amqp.Delivery, error) {
	if err := c.conn.consumeError(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.autoAck = autoAck
	c.consumerTag = consumer
	c.consuming = true
	c.deliveries = make(chan amqp.Delivery)
	return c.deliveries, nil
}

func (c *fakeChannel) Qos(prefetchCount, _ int, global bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prefetch = prefetchCount
	c.qosGlobal = global
	return nil
}

func (c *fakeChannel) Confirm(bool) error { return nil }

func (c *fakeChannel) ExchangeDeclare(name, kind string, _, _, _, _ bool, _ amqp.Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.declaredExchanges = append(c.declaredExchanges, name+":"+kind)
	return nil
}

func (c *fakeChannel) QueueDeclare(name string, _, _, _, _ bool, _ amqp.Table) (amqp.Queue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.declaredQueues = append(c.declaredQueues, name)
	return amqp.Queue{Name: name}, nil
}

func (c *fakeChannel) QueueBind(name, key, exchange string, _ bool, _ amqp.Table) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.boundQueues = append(c.boundQueues, fmt.Sprintf("%s->%s:%s", name, exchange, key))
	return nil
}

func (c *fakeChannel) NotifyClose(ch chan *amqp.Error) chan *amqp.Error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed.Load() {
		close(ch)
		return ch
	}
	c.notifyClose = append(c.notifyClose, ch)
	return ch
}

func (c *fakeChannel) NotifyReturn(ch chan amqp.Return) chan amqp.Return {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyReturn = append(c.notifyReturn, ch)
	return ch
}

func (c *fakeChannel) Close() error {
	if !c.closed.CompareAndSwap(false, true) {
		return nil
	}
	c.mu.Lock()
	deliveries, consuming := c.deliveries, c.consuming
	c.consuming = false
	c.mu.Unlock()
	if consuming {
		close(deliveries)
	}
	return nil
}

// acknowledger returns a settlement recorder to attach to a delivery.
func (c *fakeChannel) acknowledger() *fakeAcknowledger {
	return &fakeAcknowledger{settled: make(chan settlement, 1)}
}

// deliver hands one delivery to the running consumer.
func (c *fakeChannel) deliver(delivery amqp.Delivery) {
	c.mu.Lock()
	deliveries := c.deliveries
	c.mu.Unlock()
	deliveries <- delivery
}

// breakChannel simulates the broker dropping the channel, as a restart does.
// It waits for the delivery loop to register its listener, because a real
// NotifyClose is registered before the channel can fail.
func (c *fakeChannel) breakChannel(t *testing.T, err *amqp.Error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		listeners := append([]chan *amqp.Error(nil), c.notifyClose...)
		c.mu.Unlock()
		if len(listeners) > 0 {
			for _, listener := range listeners {
				select {
				case listener <- err:
				default:
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the delivery loop to watch for channel closure")
		}
		time.Sleep(time.Millisecond)
	}
}

// cancelConsumer simulates basic.cancel, which the broker sends when a queue
// the consumer is attached to disappears.
func (c *fakeChannel) cancelConsumer() {
	c.mu.Lock()
	deliveries, consuming := c.deliveries, c.consuming
	c.consuming = false
	c.mu.Unlock()
	if consuming {
		close(deliveries)
	}
}

type fakeConn struct {
	mu       sync.Mutex
	channels []*fakeChannel
	dialErr  error
	consErr  error
	pubErr   error

	dials  atomic.Int64
	closed atomic.Bool

	confirmAcked    bool
	returnOnPublish bool
}

func newFakeConn() *fakeConn {
	return &fakeConn{confirmAcked: true}
}

func (c *fakeConn) dial(context.Context) (Connection, error) {
	c.mu.Lock()
	err := c.dialErr
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	c.dials.Add(1)
	c.closed.Store(false)
	return c, nil
}

func (c *fakeConn) Channel() (Channel, error) {
	channel := &fakeChannel{conn: c}
	c.mu.Lock()
	c.channels = append(c.channels, channel)
	c.mu.Unlock()
	return channel, nil
}

func (c *fakeConn) Close() error {
	c.closed.Store(true)
	return nil
}

func (c *fakeConn) setDialErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dialErr = err
}

func (c *fakeConn) snapshotChannels() []*fakeChannel {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]*fakeChannel(nil), c.channels...)
}

func (c *fakeConn) setConsumeErr(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consErr = err
}

func (c *fakeConn) consumeError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.consErr
}

func (c *fakeConn) publishError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pubErr
}

// waitForChannel waits until at least count channels exist and returns the last
// one, which is the channel a reconnection has just created.
func (c *fakeConn) waitForChannel(t *testing.T, count int) *fakeChannel {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		channel := (*fakeChannel)(nil)
		if len(c.channels) >= count {
			channel = c.channels[len(c.channels)-1]
		}
		c.mu.Unlock()
		if channel != nil {
			channel.mu.Lock()
			consuming := channel.consuming
			channel.mu.Unlock()
			if consuming {
				return channel
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d consuming channels", count)
	return nil
}

// settledConfirmation is a publisher confirm the broker has already answered.
type settledConfirmation bool

func (c settledConfirmation) Wait(context.Context) (bool, error) { return bool(c), nil }
