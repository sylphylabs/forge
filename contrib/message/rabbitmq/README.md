# Forge RabbitMQ Transport

`contrib/message/rabbitmq` adapts RabbitMQ (AMQP 0-9-1) to
`github.com/sylphylabs/forge/transport/message` using
[`amqp091-go`](https://github.com/rabbitmq/amqp091-go).

Example:

    client, err := rabbitmq.New(
        rabbitmq.WithURL("amqp://guest:guest@127.0.0.1:5672/"),
        rabbitmq.WithBindings(map[string]rabbitmq.Binding{
            "orders.created": {
                Queue:    rabbitmq.Queue{Name: "order-worker"},
                Exchange: rabbitmq.Exchange{Name: "orders"},
            },
        }),
    )
    if err != nil {
        return err
    }
    defer client.Close()

    server := message.NewServer(client)
    if err := server.Handle("orders.created", handleOrderCreated); err != nil {
        return err
    }

    app := forge.New(forge.Server(server))
    return app.Run()

The adapter implements `message.Publisher` and `message.Subscriber`. It does
not add RabbitMQ to the root Forge module.

## Message Mapping

| `message.Message` | AMQP |
| --- | --- |
| `Body` | message body |
| `ID` | `MessageId` property |
| `Key` | routing key, mirrored into the `forge-message-key` header |
| `Headers` | headers table |

`Key` is published as the routing key because that is the only field a RabbitMQ
exchange routes on. It is mirrored into a header so it survives the round trip:
a queue bound with a wildcard receives the concrete routing key as its
destination, which would otherwise be indistinguishable from the key the
producer chose. On delivery, `Key` falls back to the routing key when the header
is absent, so messages from non-Forge producers still carry one.

Headers use the normalized multi-value `metadata.Metadata` model. A header with
several values becomes an AMQP array, because field tables have no repeated-key
form. Non-string values sent by other producers are rendered as strings rather
than dropped.

## Destination Semantics

A RabbitMQ `destination` is a **logical name**, not an address. It is a key into
the `WithBindings` map, resolving to the queue to consume from and to the
exchange and routing key to publish with. It is never matched as a pattern, and
the adapter does not split it on `.`. An unbound destination still publishes,
through the default exchange, where the routing key is a queue name.

Wildcards are therefore declared in the binding's `BindingKeys`, not in the
destination:

    rabbitmq.WithBindings(map[string]rabbitmq.Binding{
        "orders": {
            Queue:    rabbitmq.Queue{Name: "order-worker", BindingKeys: []string{"orders.#"}},
            Exchange: rabbitmq.Exchange{Name: "events", Kind: amqp.ExchangeTopic},
        },
    })

    server.Handle("orders", handleOrder)   // the logical name, not the pattern

Binding keys are evaluated by a **topic** exchange, which is the default `Kind`
when the adapter declares one. Tokens are separated by `.`:

| | Syntax | Position |
| --- | --- | --- |
| Single token | `*` | any token, and must occupy the whole token |
| Multi token | `#` | **any position**, including the middle |

`#` in the middle is legal here and is not in MQTT, so binding keys are not
portable between the two. A `direct` or `fanout` exchange ignores both
characters and treats the key literally.

The adapter passes binding keys to `QueueBind` verbatim and never parses them;
matching is entirely the broker's. Because `BindingKeys` only take effect when
the adapter declares topology, a deployment that owns its own topology declares
these bindings out of band and the adapter never sees the patterns at all.

Passing a pattern such as `orders.#` as the destination is a missing map key:
`Subscribe` fails with `ErrBindingNotFound` rather than returning a subscription
that never delivers.

A queue bound with a wildcard receives messages whose routing keys vary, so the
`destination` the core `Handler` receives is the **concrete routing key** of the
delivery, not the logical name it was registered under.

## Semantics

- **Publish** puts the channel in confirm mode and waits for the broker's
  confirm. A successful return means the broker accepted the message and routed
  it to at least one queue; with a durable queue and the default persistent
  delivery it is also on disk. It does not mean a consumer processed it.
  Cancellation after the frame is written is ambiguous, so retries still need
  idempotency keyed on `Message.ID`.
- **Unroutable messages** are published `mandatory` by default and reported as
  `ErrPublishReturned`, so a missing binding is an error rather than silent
  loss. `WithMandatoryPublish(false)` restores AMQP's discard-at-the-exchange
  behaviour.
- **Acknowledgement is manual.** A handler returning `nil` acks the delivery.
  A handler returning an error is nacked, and the disposition is chosen by
  `WithErrorClassifier`:
  - `Drop` (default) nacks with `requeue=false`. RabbitMQ then routes the
    message to the queue's dead-letter exchange if one is configured, and
    discards it otherwise. This is the default because an immediate requeue of
    a deterministically failing message spins the consumer at full speed.
  - `Requeue` nacks with `requeue=true`, for transient failures only. It needs
    a queue-level delivery limit or dead-letter policy to terminate.
- **Prefetch** defaults to 64 per consumer, applied per channel rather than
  globally. AMQP's own default is unlimited, which lets one consumer drain a
  queue into memory and defeats round-robin dispatch across replicas.
- **Handlers run one at a time per subscription**, so prefetch bounds buffering
  rather than concurrency. Register several subscriptions for parallelism.
- **Topology is not declared by default.** Exchanges, queues, and bindings are
  deployment state; an application that declares them at startup can silently
  diverge from them or fail on a mismatched redeclaration. `WithDeclare(true)`
  makes the adapter declare its configured topology, which suits tests and
  deployments that own it.
- **Subscription close** stops delivery and waits for the in-flight handler to
  settle, so shutdown never leaves a message unacknowledged. It is idempotent,
  and bounded by the caller's context.
- Queue retention, dead-letter exchanges, delivery limits, TTLs, and quorum or
  stream queue types remain infrastructure policy.

## Connection Recovery

The adapter owns one connection, opening one channel per subscription plus a
shared publishing channel. Recovery is implemented here rather than through
amqp091's built-in recovery, which is still marked experimental:

- Each subscription watches its channel with `NotifyClose` and also treats a
  closed delivery channel (a broker-side `basic.cancel`, which a deleted queue
  produces) as a signal to recover.
- On either signal the connection is dropped and the subscription redials,
  re-declares its topology when `WithDeclare` is set, reapplies prefetch, and
  re-consumes, retrying every `WithRecoveryDelay` until it succeeds or the
  subscription ends.
- The first `Subscribe` is synchronous, so a missing queue or an unreachable
  broker fails server startup instead of being retried silently. Only failures
  after that point are recovered.
- Publishing redials lazily on its next call, and discards a channel the broker
  invalidated, because AMQP closes the whole channel on a protocol error.
- Asynchronous consume, handler, acknowledgement, and recovery failures are
  reported through `WithErrorHandler` rather than a package-global logger.

Messages unacknowledged when a connection drops are redelivered by the broker,
so recovery is at-least-once. Handlers must be idempotent.

Request/reply (`reply_to` plus `correlation_id`) is deliberately absent. It is
an adapter capability, not part of the broker-neutral `transport/message`
contract.

## Tests

Unit tests use an in-process fake broker and need no RabbitMQ. Integration
tests are skipped unless `FORGE_RABBITMQ_URL` is set:

    docker run -d --name rabbit -p 5672:5672 rabbitmq:4
    FORGE_RABBITMQ_URL=amqp://guest:guest@127.0.0.1:5672/ go test ./...

The recovery test additionally needs a command that restarts that broker:

    FORGE_RABBITMQ_URL=amqp://guest:guest@127.0.0.1:5672/ \
      FORGE_RABBITMQ_RESTART="docker restart rabbit" go test ./...

End-to-end tests in `e2e_message_test.go` run the whole chain a `subscribe`
annotation produces — generated registration, a real broker, the middleware
chain, and a handler reading its destination from context. They start and remove
their own container, so they need Docker rather than a running broker:

    FORGE_MESSAGE_E2E=1 go test ./...

Point them at a broker you already have to skip the container cycle:

    FORGE_MESSAGE_E2E=1 \
      FORGE_MESSAGE_E2E_URL=amqp://guest:guest@127.0.0.1:5672/ go test ./...

They live here rather than in `internal/e2e` because that package is in the root
module, which has no AMQP dependency; testing this chain there would add a broker
driver to every Forge consumer's dependency graph.
