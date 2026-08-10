# Forge NATS Transport

`contrib/message/nats` adapts core NATS pub/sub to
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

## Subject Semantics

The two packages in this module treat `destination` differently, and the
difference is not a configuration detail.

### Core NATS: a wire address

A core NATS `destination` is the subject, passed to `conn.Subscribe` untouched.
Tokens are separated by `.`.

| | Syntax | Position |
| --- | --- | --- |
| Single token | `*` | any token, and must occupy the whole token |
| Multi token | `>` | **last token only** |

`orders.*` matches `orders.created` but not `orders.created.eu`; `orders.>`
matches both. Matching is done entirely **by the NATS server** — the adapter
neither parses, validates, nor rewrites the subject. The `destination` the core
`Handler` receives is the **concrete** published subject, not the pattern the
subscription was registered with:

    // handler receives "orders.created"
    client.Subscribe(ctx, "orders.*", handler)

A subject in another broker's syntax is passed through as-is, so the server
decides: `orders/#` is a single token containing slashes and simply matches
nothing.

### JetStream: a logical name

The [`jetstream`](./jetstream) subpackage `destination` is **not** a subject. It
is a key into the `bindings` map given to `NewSubscriber`, resolving to the
`{Stream, Consumer}` pair to attach to:

    jetstream.NewSubscriber(js, map[string]jetstream.Binding{
        "orders.created": {Stream: "ORDERS", Consumer: "order-worker"},
    })

Wildcards are therefore **not declared here at all** — the subjects that reach a
subscription come from the consumer's `FilterSubject`, which is external
deployment state the adapter never reads. Passing `orders.*` is a missing map
key, and `Subscribe` fails with `ErrBindingNotFound`. See the
[`jetstream` README](./jetstream/README.md#destination-semantics).

This package remains the ephemeral core NATS adapter. Applications that need
durable storage, explicit acknowledgement, redelivery, and duplicate detection
use the [`jetstream`](./jetstream) subpackage. JetStream is not only a server
configuration switch: publishers must wait for `PubAck`, consumers must bind to
named durable consumers, and handlers need explicit ack/nack/term behavior.
