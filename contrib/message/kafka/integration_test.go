package kafka

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/sylphylabs/forge/transport/message"
)

// envSeedBrokers gates the tests in this file. The rest of the suite runs
// against an in-process broker; these need a real cluster whose rebalance,
// replication, and offset-commit behavior an emulator cannot promise.
const envSeedBrokers = "FORGE_KAFKA_SEED_BROKERS"

// TestIntegrationRoundTrip publishes and consumes against a real Kafka cluster.
// The topic must already exist: provisioning partitions and replication factor
// is a deployment concern, not something an adapter test should decide.
func TestIntegrationRoundTrip(t *testing.T) {
	seeds := integrationSeeds(t)
	topic := integrationTopic()

	publisher, err := NewPublisher(WithPublisherSeedBrokers(seeds...))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := publisher.Close(); err != nil {
			t.Error(err)
		}
	})

	// A unique group per run starts from the topic's end, so the assertion is
	// not affected by records an earlier run left behind.
	subscriber, err := NewSubscriber(
		"forge-integration-"+time.Now().UTC().Format("20060102150405.000000000"),
		WithSubscriberSeedBrokers(seeds...),
		WithSubscriberClientOptions(kgo.ConsumeResetOffset(kgo.NewOffset().AtEnd())),
	)
	if err != nil {
		t.Fatal(err)
	}

	received := make(chan *message.Message, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sub, err := subscriber.Subscribe(ctx, topic, func(_ context.Context, _ string, msg *message.Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close(context.WithoutCancel(ctx))

	// Joining a real group takes a rebalance round; publishing immediately would
	// send to offsets the new member starting AtEnd never reads.
	time.Sleep(5 * time.Second)

	body := "integration-" + time.Now().UTC().Format(time.RFC3339Nano)
	msg := message.New([]byte(body))
	msg.ID = "integration-id"
	msg.Key = "integration-key"
	msg.SetHeader("traceparent", "00-integration")

	publishCtx, publishCancel := context.WithTimeout(ctx, 30*time.Second)
	defer publishCancel()
	if err := publisher.Publish(publishCtx, topic, msg); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-received:
		if string(got.Body) != body {
			t.Fatalf("Body = %q, want %q", got.Body, body)
		}
		if got.ID != "integration-id" {
			t.Errorf("ID = %q, want integration-id", got.ID)
		}
		if got.Key != "integration-key" {
			t.Errorf("Key = %q, want integration-key", got.Key)
		}
		if header := got.Header("traceparent"); header != "00-integration" {
			t.Errorf("traceparent = %q, want 00-integration", header)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("timed out waiting for delivery from real Kafka")
	}
}

func integrationSeeds(t *testing.T) []string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(envSeedBrokers))
	if raw == "" {
		t.Skipf("%s is not set; skipping real Kafka integration test", envSeedBrokers)
	}
	var seeds []string
	for _, seed := range strings.Split(raw, ",") {
		if seed = strings.TrimSpace(seed); seed != "" {
			seeds = append(seeds, seed)
		}
	}
	if len(seeds) == 0 {
		t.Skipf("%s contained no usable addresses", envSeedBrokers)
	}
	return seeds
}

func integrationTopic() string {
	if topic := strings.TrimSpace(os.Getenv("FORGE_KAFKA_TOPIC")); topic != "" {
		return topic
	}
	return "forge-integration"
}
