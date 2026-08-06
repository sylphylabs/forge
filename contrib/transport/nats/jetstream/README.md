# OpenKratos JetStream Transport

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

    app := kratos.New(kratos.Server(server))
    return app.Run()

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
