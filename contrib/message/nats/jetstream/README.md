# Forge JetStream Transport

`jetstream` adds durable NATS messaging without changing the protocol-neutral
`transport/message` contract.

Streams and consumers must already exist. The adapter deliberately does not
create or update production infrastructure during application startup.

Example:

    js, err := jetstream.New(conn)
    if err != nil {
        return err
    }

    publisher, err := okjs.NewPublisher(js)
    if err != nil {
        return err
    }

    subscriber, err := okjs.NewSubscriber(js, map[string]okjs.Binding{
        "orders.created": {Stream: "ORDERS", Consumer: "order-worker"},
    })
    if err != nil {
        return err
    }

    server := message.NewServer(subscriber)
    if err := server.Handle("orders.created", handleOrderCreated); err != nil {
        return err
    }

    app := forge.New(forge.WithServer(server))
    return app.Run()

## Destination Semantics

A JetStream `destination` is a **logical name**, not a subject. It is a key into
the `bindings` map above, resolving to the `{Stream, Consumer}` pair to attach
to, and is never sent to the server as an address. That `"orders.created"` reads
like a subject is a naming convention; the adapter treats it as an opaque key
and does not split it on `.`.

Wildcards are therefore **not declared here at all**. Which subjects reach a
subscription is decided by the consumer's `FilterSubject` and the stream's
`Subjects` — external deployment state. Since the adapter never creates or
updates streams or consumers, it can neither read those filters nor validate a
destination against them.

Passing a subject pattern such as `orders.*` as a destination is simply a
missing map key: `Subscribe` fails with `ErrBindingNotFound` rather than
returning a subscription that never delivers.

This differs from the core NATS adapter in the parent package, where the
destination *is* the subject and `*` and `>` are evaluated by the server.

## Semantics

- `Publisher.Publish` waits for a JetStream `PubAck`.
- A non-empty `Message.ID` is also sent as `Nats-Msg-Id`, allowing stream-level
  duplicate detection when the stream has a duplicate window configured.
- Successful handlers use confirmed acknowledgement by default.
- Handler errors use delayed negative acknowledgement by default. Applications
  can classify permanent failures as `Terminate`.
- Stream retention, replicas, duplicate windows, consumer filters, ack wait,
  maximum delivery attempts, backoff, and dead-letter handling remain explicit
  infrastructure or application policy.
- Cancellation stops consumption promptly. Explicit close drains buffered
  deliveries until the supplied shutdown context expires.

JetStream provides durable delivery and acknowledgement. It does not make a
database transaction atomic with message publication. Transactional outbox and
consumer inbox/idempotency remain application persistence concerns.
