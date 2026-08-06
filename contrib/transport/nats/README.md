# Forge NATS Transport

`contrib/transport/nats` adapts core NATS pub/sub to
`github.com/sylphylabs/forge/transport/message`.

Example:

    client, err := nats.New(nats.WithURL("nats://127.0.0.1:4222"))
    if err != nil {
        return err
    }
    defer client.Close()

    server := message.NewServer(client)
    if err := server.Handle("accounts.created", handleAccountCreated); err != nil {
        return err
    }

    app := forge.New(forge.Server(server))
    return app.Run()

The adapter implements `message.Publisher` and `message.Subscriber`. It does
not add NATS to the root Forge module.

## Semantics

- `Publish` sends a core NATS message and then calls `FlushWithContext`. A
  successful return means the NATS server responded to the flush after the
  publish. It does not imply JetStream durability. A timeout or cancellation
  after the local publish is an ambiguous outcome: the server may already have
  received the message, so callers need an idempotency strategy before retrying.
- `Subscribe` binds the NATS subscription lifetime to the context passed by
  `message.Server`. Cancellation unsubscribes and stops later delivery.
- Handler errors are reported through `WithErrorHandler`; core NATS has no
  acknowledgement decision to return to the server.
- `Request` is provided as a NATS-specific helper. Request/reply is not part of
  the broker-neutral `transport/message` contract.
- `Message.ID` and `Message.Key` are carried in Forge-owned NATS headers.
  Other headers use the normalized multi-value `metadata.Metadata` model.

This package remains the ephemeral core NATS adapter. Applications that need
durable storage, explicit acknowledgement, redelivery, and duplicate detection
use the [`jetstream`](./jetstream) subpackage. JetStream is not only a server
configuration switch: publishers must wait for `PubAck`, consumers must bind to
named durable consumers, and handlers need explicit ack/nack/term behavior.
