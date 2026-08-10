# Asynchronous Message Transport

Status: accepted core contract; the first broker adapter is an optional nested
module.

Last reviewed: July 24, 2026

## Decision

Forge adopts a small protocol-neutral asynchronous message contract in
[`transport/message`](../../transport/message). It absorbs the useful parts of
`tx7do/forge-transport` without copying its broker abstraction or SDK
implementations.

The root module owns only:

- an encoded `Message` with stable identity, key, normalized multi-value headers,
  and byte payload;
- `Publisher`, `Subscriber`, and `Subscription` interfaces;
- a typed `Handler` and middleware chain;
- a `Server` that owns subscriptions and implements `transport.Server`.

Kafka, NATS, RabbitMQ, MQTT, task queues, and their client options remain
optional nested modules. They must not enlarge applications that only use HTTP
and gRPC.

Adapters live under `contrib/message/<broker>`, following the convention the
rest of `contrib` uses: the first path element names the core contract being
implemented, the second names who implements it — as in `contrib/config/etcd`
and `contrib/registry/consul`. A broker adapter implements `message.Publisher`
and `message.Subscriber`, so it belongs under `message` rather than under
`transport`, where `contrib/transport/mcp` implements `transport.Server`
instead.

The first official adapter is
[`contrib/message/nats`](../../contrib/message/nats). It validates the core
contract against a real NATS server without adding the NATS SDK to the root
module.

The same nested module contains two deliberately separate modes:

- the root `nats` package provides ephemeral core NATS pub/sub and
  request/reply;
- the `nats/jetstream` package provides durable publish and consumption against
  existing Streams and durable Consumers.

Adopting JetStream is therefore not just enabling `jetstream: true` on a NATS
server. The application adapter must use the JetStream publish API, wait for a
`PubAck`, bind handlers to durable consumers, and make explicit ack, retry, and
termination decisions.

## Why This Boundary

The useful tx7do ideas are independent module isolation, a common lifecycle,
typed handlers, middleware composition, and propagation metadata. Its generic
broker API also exposes `any` payloads, arbitrary metadata, raw SDK messages,
and broker-specific delivery fields. Those fields make a portable contract
look simpler while moving type and acknowledgement errors to runtime.

Forge therefore keeps the portable envelope deliberately small:

```go
type Publisher interface {
	Publish(context.Context, string, *Message) error
}

type Subscriber interface {
	Subscribe(context.Context, string, Handler) (Subscription, error)
}

type Subscription interface {
	Close(context.Context) error
}

type Handler func(context.Context, string, *Message) error
```

The handler's string is the concrete delivery destination. It remains useful
when the registered subscription uses a wildcard.

The API does not include a universal `Request` method. Request/reply is an
adapter capability, not a semantic guarantee of Kafka, task queues, or all
pub/sub systems.

## Ownership and Lifecycle

`Subscriber.Subscribe` receives the subscription lifetime context. An adapter
must stop delivery when that context is canceled. `Subscription.Close` is an
explicit, bounded shutdown operation and must be idempotent.

`message.Server` owns every subscription returned by `Subscribe`:

1. registrations are created in declaration order;
2. a failed registration closes already-created subscriptions;
3. shutdown cancels delivery and closes subscriptions in reverse order;
4. all close errors are returned with `errors.Join`;
5. registration and middleware configuration freeze at `Start`.

The server does not install signals, global loggers, telemetry providers, or
background goroutines that outlive its parent context. It can therefore be
embedded in an existing `forge.App` or run independently in tests.

## Message Ownership

`message.New` copies the supplied body. `Message.Clone` copies the body and
headers for handlers that need to retain a message after the callback returns.
Adapters may reuse their receive buffer only after the handler returns. The
core message has no partition, offset, raw SDK object, or acknowledgement
method; adapters translate those concepts at their boundary.

Headers use `metadata.Metadata`, so names are normalized and values preserve
their multiplicity. OpenTelemetry propagation can be implemented by an
adapter-specific carrier over these headers without putting an OTel provider
or global propagator in the root module.

The optional
[`contrib/otel/message`](../../contrib/otel/message) package now provides that
implementation as a protocol-neutral decorator:

```go
publisher := messageotel.NewPublisher(
	nextPublisher,
	messageotel.WithTracerProvider(provider),
	messageotel.WithPropagator(propagation.TraceContext{}),
	messageotel.WithSystem("nats"),
)
```

`NewPublisher` creates a producer span and injects its context into a cloned
message. `messageotel.Consumer` extracts the context from headers and creates a
child process span around the typed handler. Both preserve the wrapped return
value and record handler/publish errors with error status. Provider, propagator,
and broker system are explicit instance options; the package does not read OTel
globals or guess the broker from an adapter type. Span attributes are limited to
the messaging system, destination, operation type/name, and optional message
ID. Payloads and arbitrary header values are intentionally excluded to keep
cardinality and data exposure bounded.

## Middleware and Error Semantics

Message middleware is typed as `func(Handler) Handler`; it does not use the
existing `any` request ABI. Middleware receives the concrete delivery
destination. The chain preserves declaration order and leaves
acknowledgement, retry, dead-letter, and negative-ack policy to the adapter.
A handler error is therefore an outcome for the adapter to classify, not an
implicit acknowledgement decision made by Forge.

Generated protobuf operation metadata may later add a typed codec/handler
adapter. It must reuse this message lifecycle and operation identity instead
of introducing a second broker selector or descriptor parser.

## Generated Subscriptions

`cmd/protoc-gen-go-message` turns a `(sylphy.message.v1.subscribe)` method
option into a typed server interface and a registration function over this
lifecycle. It emits `_message.pb.go` and calls `Server.Handle`; it does not
introduce a second lifecycle, broker selector, or descriptor parser.

```proto
service OrderEvents {
  rpc OnOrderCreated(OrderCreated) returns (google.protobuf.Empty) {
    option (sylphy.message.v1.subscribe) = {destination: "order.created"};
  }
}
```

```go
type OrderEventsMessageServer interface {
	OnOrderCreated(context.Context, *OrderCreated) error
}

func RegisterOrderEventsMessageServer(
	s *message.Server,
	srv OrderEventsMessageServer,
	opts ...OrderEventsMessageRegisterOption,
) error
```

The proto `destination` is required: a method name is not a valid topic in
every broker, so the generator rejects an annotation without one rather than
inventing a default. That destination is the contract's default, not a fixed
value. The same contract runs against different topic prefixes because
registration can override it:

```go
err := RegisterOrderEventsMessageServer(server, srv,
	WithOrderEventsMessageDestinationPrefix("staging."),
	WithOrderEventsMessageDestination("OnOrderCreated", "legacy.orders.created"),
)
```

`WithXxxMessageDestination` keys on the RPC name declared in the proto file,
which stays stable when the Go method name is remapped, and it replaces the
destination outright rather than being prefixed again.

The generated handler decodes the message body with `proto.Unmarshal` and calls
the service method. A handler that needs the destination reads it from the
transport in context with `message.DestinationFromServerContext(ctx) (string,
bool)`, the same accessor a hand-written handler uses; there is no per-service
generated accessor, because the destination is a property of the delivery rather
than of the contract. Under a wildcard subscription it is the concrete
destination that delivered the message, not the pattern that matched it.

A request that is not a `*message.Message` is an error rather than an empty
decode: the only way to produce one is to mount the handler outside a message
server, and a zero-value request would reach business code as an event that
never arrived.

Streaming RPCs are rejected: the portable contract delivers one encoded message
per callback and has no stream semantics to generate against. No two methods in
a file may declare the same destination, across services as well as within one,
because the second `Handle` would silently shadow the first. Registration
re-checks this after overrides are applied, since an override can collide two
destinations that the schema kept apart.

## Adapter and Repository Layout

An official adapter belongs in a nested module such as the current NATS
adapter:

```text
contrib/message/nats/
  go.mod
  nats.go
  README.md
  nats_test.go
```

The adapter owns its SDK, connection and reconnection policy, delivery
semantics, and protocol conformance tests. It implements `message.Publisher`
and/or `message.Subscriber`, accepts application-owned callbacks and
dependencies instead of process globals, and proves cancellation and close
behavior without a repository `go.work`.

### JetStream Adapter

`contrib/message/nats/jetstream` reuses the portable `message.Message` and
lifecycle interfaces, but keeps durable delivery policy at the NATS boundary.
It does not create or update Streams or Consumers during application startup.
Deployment automation owns retention, storage, replicas, duplicate windows,
filters, acknowledgement wait, maximum deliveries, backoff, and dead-letter
handling.

Each application destination maps explicitly to an existing Stream and pull
Consumer:

```go
subscriber, err := jetstream.NewSubscriber(js, map[string]jetstream.Binding{
	"orders.created": {Stream: "ORDERS", Consumer: "order-worker"},
})
```

The publisher waits for `PubAck`. A non-empty `Message.ID` is sent as
`Nats-Msg-Id`, so a configured Stream duplicate window can reject repeated
publishes with the same ID. A successful handler uses confirmed acknowledgement
by default. A handler error is negatively acknowledged with a configurable
delay by default; an application classifier can terminate a permanently invalid
message instead. Asynchronous consume, handler, and acknowledgement failures are
reported through an injected callback rather than a package-global logger.

JetStream changes delivery from best-effort pub/sub to durable at-least-once
processing; it does not create exactly-once business effects. In particular, it
cannot atomically commit an application database transaction and publish to
NATS. Producers that require that boundary use a transactional outbox in the
same database transaction, then publish with the outbox event ID as
`Message.ID`. Consumers use an inbox or another database uniqueness constraint
keyed by that ID before applying side effects. Stream duplicate detection is a
useful retry optimization, not a replacement for either persistence pattern.

The default project layout remains a small single-module HTTP/gRPC service.
Broker adapters are opt-in examples or nested modules, not dependencies of a
new service template. A multi-service `go.work` example may demonstrate them,
but it is not the required starting point.

## Deliberately Excluded

- a universal `Broker` interface with `any Body`, `any Msg`, or
  `map[string]any` metadata;
- a context hidden in subscription options;
- package-global loggers, registries, or providers;
- `context.Background()` inside delivery loops;
- fake network endpoints for non-network keepalive transports;
- implicit asynchronous publish success;
- direct source copying from `tx7do/forge-transport`.

## Acceptance Evidence

The first implementation is covered by `transport/message` tests for:

- body and header ownership and cloning;
- middleware ordering and error propagation;
- reverse-order shutdown;
- subscription failure rollback;
- joined close errors;
- parent-context cancellation;
- configuration validation and post-start freezing.

The NATS nested module adds an in-process real NATS server fixture covering:

- core publish/subscribe and concrete delivery subjects;
- body, message ID, key, and multi-value header propagation;
- server metadata injection for middleware and tracing carriers;
- subscription cancellation and idempotent close;
- asynchronous handler error reporting without a package-global logger;
- adapter-specific request/reply without widening the portable interfaces;
- owned versus application-injected connection shutdown.

The optional OTel message decorator adds focused in-memory span evidence for:

- producer trace-context injection without mutating the original message;
- consumer parent extraction and producer/consumer span kinds;
- semantic messaging attributes and destination-based span names;
- publish and handler error recording;
- custom providers/propagators and nil-message transparency.

The JetStream subpackage extends that evidence with a real file-backed
JetStream fixture covering:

- publish acknowledgement, stream sequence, and duplicate detection;
- body, ID, key, multi-value headers, and server metadata propagation;
- confirmed successful acknowledgement;
- delayed redelivery after retryable handler failure;
- termination of permanent handler failure;
- refusal to provision a missing Stream or Consumer implicitly;
- cancellation, draining, idempotent close, and `message.Server` integration.

`cmd/protoc-gen-go-message` adds descriptor-level evidence for annotation
discovery, rejection of a missing/blank destination, rejection of streaming and
duplicate destinations, and omission of unannotated methods. A committed
`internal/testdata/orderevents` fixture compiles the generated output and
proves against a real `message.Server` that:

- declared destinations are the ones bound;
- a per-operation override replaces a destination outright;
- a prefix applies to every destination an override did not replace;
- the handler decodes the body and propagates the server's error;
- an undecodable body fails without reaching the server;
- the delivery destination is readable from the handler context, and is the
  concrete one when it differs from the destination that was bound;
- a registration that cannot be honoured binds nothing at all, rather than
  returning an error and leaving a server that starts on half its contract;
- an override naming an operation the contract does not declare is rejected;
- a request that is not the delivered envelope is an error, not an empty event.

This proves the first wire adapter and the root/nested-module dependency
boundary, including JetStream acknowledgement and redelivery behavior. It does
not prove database/message atomicity, Kafka rebalance and offset behavior, or
RabbitMQ acknowledgement semantics.
