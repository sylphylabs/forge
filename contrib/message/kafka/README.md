# Forge Kafka Transport

`contrib/message/kafka` adapts Kafka topics to
`github.com/sylphylabs/forge/transport/message` using
[franz-go](https://github.com/twmb/franz-go).

Example:

    publisher, err := kafka.NewPublisher(
        kafka.WithPublisherSeedBrokers("127.0.0.1:9092"),
    )
    if err != nil {
        return err
    }
    defer publisher.Close()

    subscriber, err := kafka.NewSubscriber("order-worker",
        kafka.WithSubscriberSeedBrokers("127.0.0.1:9092"),
    )
    if err != nil {
        return err
    }

    server := message.NewServer(subscriber)
    if err := server.Handle("orders.created", handleOrderCreated); err != nil {
        return err
    }

    app := forge.New(forge.WithServer(server))
    return app.Run()

The adapter implements `message.Publisher` and `message.Subscriber`. It does not
add Kafka to the root Forge module.

## Semantics

- `Publish` uses `ProduceSync`, so a successful return means the broker returned
  the acks the client is configured for. A timeout or cancellation after the
  record left the client is an ambiguous outcome: the broker may already have
  stored it, so callers need an idempotency strategy before retrying.
- `Message.Body` maps to the record value, `Message.Key` to the record key, and
  `Message.Headers` to record headers. An empty `Key` is sent as a nil record
  key, which lets the partitioner distribute records instead of pinning them to
  one partition.
- `Message.ID` travels in the `forge-message-id` record header, mirroring how
  the NATS adapter carries identity. Kafka has no native message identity, and
  it does not deduplicate on this value; producer idempotence only removes
  duplicates from retries inside one producer session.
- Delivery is **at-least-once**. Automatic committing is disabled and offsets
  commit only after a handler returns nil.
- A handler error stops the current batch. Records that already succeeded stay
  committed, the failing record and the rest of the poll stay uncommitted, and
  consumption resumes at the failing offset after the next rebalance or restart.
  The adapter does not retry in place, because an in-process retry loop silently
  stalls the partition. Backoff, dead-letter topics, and poison-message
  detection remain application policy, driven by `WithErrorHandler`.
- Records are handled one at a time in fetch order, preserving Kafka's
  per-partition ordering. No ordering is assumed across partitions.
- The consumer group is subscriber-wide. Every destination is consumed by the
  same group, so extra replicas divide partitions rather than duplicating
  delivery.
- Each `Subscribe` creates its own franz-go client, because `message.Server`
  registers destinations one at a time and closes each subscription
  independently. A shared client would let one destination's shutdown revoke
  another's partitions.
- `BlockRebalanceOnPoll` is enabled so a batch cannot be committed to partitions
  that were reassigned mid-processing. Handlers must therefore stay fast enough
  to avoid the rebalance timeout; use `WithMaxPollRecords` to bound batch size.
- Cancellation stops polling. `Subscription.Close` leaves the group and waits
  for the in-flight batch, so no handler runs after it returns.
- Offset commits use a context detached from the subscription lifetime. Shutdown
  must still checkpoint work whose handlers already succeeded, otherwise every
  restart guarantees redelivery of the whole in-flight batch.

## Topic Semantics

A Kafka `destination` is a **wire address**: the literal topic name handed to
`kgo.ConsumeTopics`. It has **no separator and no hierarchy** — a dot in
`orders.created` is an ordinary character, not a level boundary — and therefore
**no wildcards**.

`Subscribe` rejects a topic containing `*`, `#`, or `>` with
`ErrWildcardSubscribe`. Those are the wildcards of NATS, RabbitMQ, and MQTT, none
of them is legal in a Kafka topic name, and a literal match against such a name
would join the consumer group and never receive a record. Failing at
registration is the point: a subscription that never delivers produces no later
signal to fail on. MQTT's `+` is not screened, being legal in a topic name and a
wildcard only within a filter.

Consuming by pattern is available, but must be asked for:

    subscriber, err := kafka.NewSubscriber("order-worker",
        kafka.WithSubscriberSeedBrokers("127.0.0.1:9092"),
        kafka.WithTopicRegex(),
    )
    // the destination is now a regular expression
    server.Handle(`orders\..*`, handleOrder)

`WithTopicRegex` enables `kgo.ConsumeRegex` and lifts the rejection in one
option, because `kgo.Opt` is opaque and the adapter cannot otherwise tell that
regex consumption was requested. This is not a hierarchical wildcard: the
pattern is matched against topic *names*, so a newly created topic only joins the
subscription after the client's next metadata refresh.

`Publish` is not restricted. A literal topic name containing `*` is unusual but
valid to produce to, and the publish side has no pattern to misread.

Request/reply is deliberately absent. It is not part of the broker-neutral
`transport/message` contract, and Kafka has no native request/reply primitive.

Topic creation, partition count, replication factor, and retention remain
deployment concerns. The adapter never provisions topics.

## Tests

Unit and end-to-end tests run against an in-process broker (`kfake`) and need no
external services. Tests that require a real cluster are gated:

    FORGE_KAFKA_SEED_BROKERS=127.0.0.1:9092 go test ./...

They skip by default. `FORGE_KAFKA_TOPIC` overrides the topic, which must
already exist.
