# Forge MQTT 5 Transport

`contrib/message/mqtt` adapts MQTT 5 publish/subscribe to
`github.com/sylphylabs/forge/transport/message`. It uses
[`eclipse/paho.golang`](https://github.com/eclipse/paho.golang), which speaks
MQTT 5; the older `paho.mqtt.golang` client is MQTT 3.1.1 and is not used.

Example:

    client, err := mqtt.New(ctx,
        mqtt.WithURL("mqtt://127.0.0.1:1883"),
        mqtt.WithClientID("orders-worker"),
        mqtt.WithPublishQoS(1),
        mqtt.WithSubscribeQoS(1),
    )
    if err != nil {
        return err
    }
    defer client.Close(ctx)

    server := message.NewServer(client)
    if err := server.Handle("accounts/+/created", handleAccountCreated); err != nil {
        return err
    }

    app := forge.New(forge.WithServer(server))
    return app.Run()

The adapter implements `message.Publisher` and `message.Subscriber`. It does
not add MQTT to the root Forge module.

## Message Mapping

| `message.Message` | MQTT 5 |
| --- | --- |
| `Body` | PUBLISH payload |
| `Headers` | User Properties, preserving order and duplicates |
| `ID` | Correlation Data, or a user property with `WithIDInUserProperty` |
| `Key` | the `forge-message-key` user property |

MQTT has no native message key: the topic is the only routing dimension, and
ordering is per-topic per-session rather than per-key. `Key` is therefore a
portable label that survives a round trip, not a partitioning or ordering
guarantee. Correlation Data carries `ID` by default because it is the MQTT 5
field for a message's identity; this is not MQTT request/response, which the
broker-neutral contract deliberately excludes.

Forge-owned properties are stripped from `Headers` on receipt, so a round trip
does not duplicate them as both typed fields and headers.

## QoS and Acknowledgement

`WithPublishQoS` and `WithSubscribeQoS` accept 0, 1, or 2.

- At QoS 0 a successful `Publish` means the packet was written to the
  connection. The broker never confirms it.
- At QoS 1 and 2 `Publish` waits for the broker's acknowledgement and fails on
  a rejecting reason code. A cancellation or timeout after the packet is
  written is ambiguous at every QoS, so retries need an idempotency strategy.
- `Subscribe` reports a SUBACK that denies the filter. A reason code below
  `0x80` is the granted QoS, which may be lower than requested and is still a
  success.

MQTT 5 gives a subscriber no negative acknowledgement, and a PUBACK reason code
does not ask the broker to resend. The only redelivery signal is an
acknowledgement that is never sent. A handler error therefore **withholds** the
acknowledgement, so the message is redelivered when the session resumes. That
requires a durable session:

    mqtt.WithCleanStart(false)
    mqtt.WithSessionExpiry(300)
    mqtt.WithClientID("stable-id")

With the default clean start, a withheld acknowledgement is only redelivered
while the connection lives; the session ends at disconnect and the message is
discarded. `WithAckOnError(true)` acknowledges failed messages instead, which
suits handlers that treat their own errors as permanent. `WithErrorHandler`
observes the failure either way, because an error cannot be returned to a
broker.

Acknowledgements are batched: MQTT requires them in receipt order, so paho
flushes them on a ticker (`WithAckInterval`). A message handled successfully
within that window of `Close` is redelivered on the next session. Draining
before shutdown is an application decision.

At QoS 0 there is no acknowledgement packet, so a handler error has no protocol
effect and the message is simply gone.

## Topic Semantics

An MQTT `destination` is a **wire address**: the topic filter sent to the broker
in SUBSCRIBE. Levels are separated by `/`.

| | Syntax | Position |
| --- | --- | --- |
| Single level | `+` | any level, and must occupy the whole level |
| Multi level | `#` | **last level only**, and must occupy it |

`sport/+/player1` matches `sport/tennis/player1`. `sport/#` matches
`sport/tennis/player1`, and also matches the bare parent topic `sport` — a
multi-level wildcard covers the level it replaces (MQTT 5 §4.7.1.2). `sport/#/x`
is not a valid filter, and a broker that accepts it still matches nothing.
Neither character is a wildcard when it appears inside a level: `sport+` is
literal.

Wildcards are matched **by the adapter**, not only by the broker. One MQTT
connection multiplexes every subscription, so the adapter keeps its own filter
table and routes each delivered topic through it; acknowledgement depends on the
aggregate outcome of every handler a topic matched, which the paho router cannot
express. The `destination` the core `Handler` receives is always the **concrete**
topic that produced the delivery, never the filter it was registered with:

    // handler receives "accounts/42/created"
    client.Subscribe(ctx, "accounts/+/created", handler)

Because `/` is the only separator, a filter written in another broker's syntax is
one literal level: `orders.*` subscribes to a topic named `orders.*` and receives
nothing. The adapter cannot reject it, since that is a legal MQTT topic name.

`Publish` rejects a topic containing `+` or `#` with `ErrWildcardPublish`, because
MQTT forbids wildcards in a PUBLISH topic name.

## Connection and Reconnect

`New` owns the connection unless `WithConnectionManager` supplies one, in which
case `Close` leaves it connected. Reconnection is handled by `autopaho` with its
own backoff; subscriptions are re-sent on every reconnect rather than only when
the broker reports no session, because a broker may expire a session the client
believes is durable. `SUBSCRIBE` is idempotent for a given filter, so this
replaces an identical subscription on a resumed session.

`Subscribe` binds the subscription lifetime to the context passed by
`message.Server`; cancellation unsubscribes and stops later delivery.

## Tests

Unit tests run against an in-process seam and need no broker. Integration tests
skip unless `FORGE_MQTT_BROKER_URL` names a running MQTT 5 broker:

    docker run -d -p 1883:1883 eclipse-mosquitto:2 \
      mosquitto -c /mosquitto-no-auth.conf
    FORGE_MQTT_BROKER_URL=mqtt://127.0.0.1:1883 go test ./...
