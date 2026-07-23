# Asynchronous Message Transport

Status: accepted core contract; broker adapters are not yet part of the root
module.

Last reviewed: July 23, 2026

## Decision

OpenKratos adopts a small protocol-neutral asynchronous message contract in
[`transport/message`](../../transport/message). It absorbs the useful parts of
`tx7do/kratos-transport` without copying its broker abstraction or SDK
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

## Why This Boundary

The useful tx7do ideas are independent module isolation, a common lifecycle,
typed handlers, middleware composition, and propagation metadata. Its generic
broker API also exposes `any` payloads, arbitrary metadata, raw SDK messages,
and broker-specific delivery fields. Those fields make a portable contract
look simpler while moving type and acknowledgement errors to runtime.

OpenKratos therefore keeps the portable envelope deliberately small:

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
embedded in an existing `kratos.App` or run independently in tests.

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

## Middleware and Error Semantics

Message middleware is typed as `func(Handler) Handler`; it does not use the
existing `any` request ABI. Middleware receives the concrete delivery
destination. The chain preserves declaration order and leaves
acknowledgement, retry, dead-letter, and negative-ack policy to the adapter.
A handler error is therefore an outcome for the adapter to classify, not an
implicit acknowledgement decision made by OpenKratos.

Generated protobuf operation metadata may later add a typed codec/handler
adapter. It must reuse this message lifecycle and operation identity instead
of introducing a second broker selector or descriptor parser.

## Adapter and Repository Layout

An official adapter belongs in a nested module such as:

```text
contrib/transport/nats/
  go.mod
  transport.go
  README.md
  transport_test.go
```

The adapter owns its SDK, connection and reconnection policy, delivery
semantics, and protocol conformance tests. It implements `message.Publisher`
and/or `message.Subscriber`, accepts an injected logger/telemetry dependency,
and proves cancellation and close behavior without a repository `go.work`.

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
- direct source copying from `tx7do/kratos-transport`.

## Acceptance Evidence

The first implementation is covered by `transport/message` tests for:

- body and header ownership and cloning;
- middleware ordering and error propagation;
- reverse-order shutdown;
- subscription failure rollback;
- joined close errors;
- parent-context cancellation;
- configuration validation and post-start freezing.

The next adapter work must add an external broker or protocol conformance
fixture. A fake subscriber is sufficient for the core lifecycle contract but
does not prove Kafka, NATS, or RabbitMQ wire behavior.
