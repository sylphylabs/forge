# Middleware

Package `github.com/sylphylabs/forge/middleware`.

**Server middleware has two layers.** A server-wide option on the transport
server wraps everything the server serves; the generated per-service plan
wraps individual services and methods. Anything method-aware belongs in the
plan — the server-wide layer never sees which method is running until the
transport context says so.

Client middleware is a client option: `WithClientMiddleware(...)`.

Rationale and the full contract are in
[docs/design/generated-middleware.md](../design/generated-middleware.md).

## The two contracts

Unary and stream middleware are separate types. There is no single
`middleware.Middleware`.

```go
type UnaryHandler func(ctx context.Context, req any) (any, error)
type UnaryMiddleware func(UnaryHandler) UnaryHandler

type ServerStream interface {
	Context() context.Context
	SendMsg(any) error
	RecvMsg(any) error
}
type StreamHandler func(request any, stream ServerStream) error
type StreamMiddleware func(StreamHandler) StreamHandler
```

A `StreamMiddleware` wraps **one complete stream lifecycle**, not one message.
`request` is the decoded initial request for a server-streaming method and `nil`
for client-streaming and bidirectional methods, whose messages arrive through
`RecvMsg`.

## Writing unary middleware

```go
func Tagging(value string) middleware.UnaryMiddleware {
	return func(next middleware.UnaryHandler) middleware.UnaryHandler {
		return func(ctx context.Context, req any) (any, error) {
			ctx = context.WithValue(ctx, tagKey{}, value)
			return next(ctx, req)
		}
	}
}
```

The outer function body runs **once, at wrapper construction**. Do expensive
setup there, not in the returned handler. Call information comes from the
transport:

```go
if tr, ok := transport.FromServerContext(ctx); ok {
	_ = tr.Operation()     // opaque; label with it, never parse it
	_ = tr.Kind()          // transport.KindHTTP, transport.KindGRPC
	_ = tr.RequestHeader()
}
```

## Attaching server middleware

### Server-wide

Each transport server takes middleware as a construction option, composed
exactly once inside `NewServer`:

```go
httpSrv := forgehttp.NewServer(
	forgehttp.WithMiddleware(tracing.Server(), logging.Server(logger)),
)

grpcSrv := forgegrpc.NewServer(
	forgegrpc.WithMiddleware(tracing.Server(), logging.Server(logger)),
	forgegrpc.WithStreamMiddleware(streamAuth(authenticator)),
)

msgSrv := message.NewServer(subscriber,
	message.WithMiddleware(tracing.Server(), logging.Server(logger)),
)
```

Semantics, identical across transports:

- Server-wide middleware runs **outside** (before) everything attached through
  a generated plan, for every operation the server serves.
- Composition happens once, at construction. There is no way to add middleware
  to a running server.
- A `nil` middleware, or one returning a `nil` handler, fails construction;
  the error is reported by `Start` (and `Endpoint`), the way a bad listener is.
- On HTTP the layer runs after routing but before the body is decoded: the
  handler's request argument is the `*http.Request` and the reply is `nil`.
  On gRPC the unary request is the decoded protobuf message.
- gRPC has both `Middleware` (unary methods) and `StreamMiddleware` (streaming
  methods; the initial request is not yet decoded at this layer, so the stream
  handler's `request` is always `nil`). HTTP has no server-wide stream layer:
  an HTTP stream is created by the handler itself (SSE or WebSocket upgrade),
  after this layer has already run.

Use this layer for concerns that are truly server-global — tracing, logging,
metadata. Anything per-service or per-method goes in the generated plan.

### Per-service (generated)

`protoc-gen-go-middleware` emits a plan type per service. The zero value is
usable. Build the wrapped service, then register it.

```go
service, err := v1.WrapGreeterHTTPServer(greeter, v1.GreeterMiddleware{
	Unary: []middleware.UnaryMiddleware{
		recovery.Recovery(),
		tracing.Server(),
		logging.Server(logger),
	},
	Stream: []middleware.StreamMiddleware{
		recovery.Stream(),
		streamAuth(authenticator),
	},
	Methods: v1.GreeterMethodMiddleware{
		SayHello:       []middleware.UnaryMiddleware{requireScope("greet")},
		SayHelloStream: []middleware.StreamMiddleware{throttleStream()},
	},
})
if err != nil {
	return err
}
v1.RegisterGreeterHTTPServer(srv, service)
```

`Unary` middleware never runs for streaming methods: stream behaviour comes
only from the `Stream` slice (for example `recovery.Stream()` to classify a
panic before the transport backstop turns it into a generic internal error).

gRPC is identical in shape, with the same plan value reused:

```go
service, err := v1.WrapGreeterGRPCServer(greeter, plan)
if err != nil {
	return err
}
v1.RegisterGreeterServer(grpcSrv, service)
```

Semantics, all fixed:

- `Unary` applies to every unary method; `Stream` to every streaming method.
- A field under `Methods` **appends** for that method; service middleware runs
  before method middleware.
- The first middleware in a slice is the outermost wrapper and runs first.
- A `nil` middleware, or one returning a `nil` handler, fails `Wrap...` — check
  the error; it cannot fail at request time.
- Composition happens once, at construction. Wrappers snapshot every slice, so
  mutating the plan afterwards has no effect.
- An empty plan applies nothing.

## Writing stream middleware

Per-message behaviour comes from decorating `ServerStream`, not from a
per-message hook:

```go
type countingStream struct {
	middleware.ServerStream
	received *atomic.Int64
}

func (s *countingStream) RecvMsg(m any) error {
	if err := s.ServerStream.RecvMsg(m); err != nil {
		return err // includes io.EOF on half-close
	}
	s.received.Add(1)
	return nil
}

func Counting(received *atomic.Int64) middleware.StreamMiddleware {
	return func(next middleware.StreamHandler) middleware.StreamHandler {
		return func(request any, stream middleware.ServerStream) error {
			return next(request, &countingStream{ServerStream: stream, received: received})
		}
	}
}
```

Add context values the same way, by overriding `Context()`. There is no
`SetContext`. A middleware may pass a replacement initial request to `next`, but
it must keep the generated request type; the terminal adapter returns a
qualified error rather than panicking on a mismatch.

`ServerStream` is deliberately smaller than `grpc.ServerStream`. HTTP response
control, gRPC peer info, and native headers are reached through the
transport-specific layer, not here.

## Client middleware

```go
conn, err := forgegrpc.NewClient(ctx,
	forgegrpc.WithTarget("discovery:///greeter"),
	forgegrpc.WithClientMiddleware(tracing.Client(), retryMiddleware),
)

client, err := forgehttp.NewClient(ctx,
	forgehttp.WithTarget(endpoint),
	forgehttp.WithClientMiddleware(tracing.Client()),
)
```

## Built-in middleware

| Package | Constructor | Side |
| --- | --- | --- |
| `middleware/recovery` | `Recovery(opts...)` / `Stream(opts...)` | server |
| `middleware/logging` | `Server(logger)` / `Client(logger)` | both |
| `middleware/validate` | `Validator(validators...)` | server |
| `middleware/ratelimit` | `Server(opts...)` | server |
| `middleware/timeout` | `Server(opts...)` | server |
| `middleware/metadata` | `Server(opts...)` / `Client(opts...)` | both |
| `middleware/retry` | `Client(opts...) (UnaryMiddleware, error)` | client |
| `middleware/circuitbreaker` | `Client(opts...)` | client |
| `middleware/selector` | `Client(ms...) *Builder` | client |
| `contrib/otel/tracing` | `Server(opts...)` / `Client(opts...)` | both |

`timeout.Server` is unary middleware: it bounds unary calls only. A stream is
never bounded by it — a stream that needs a maximum lifetime must set one
explicitly.

Every transport also carries a built-in, non-removable panic backstop at its
outermost layer, outside even server-wide middleware: a panic anywhere below
is logged with its value and stack, and the client receives a generic
`KindInternal` error that never contains the panic text. `recovery` remains
the customization layer — it runs inside the chains, so it handles the panic
first and can classify it (`WithHandler`), attach context, or use its own
logger. The backstop only guarantees survival and non-disclosure.

`retry.Client` returns an error — it validates its policy at construction. It
retries only with evidence the request never reached a server, or with the
caller's per-call idempotence declaration: `ctx = retry.Idempotent(ctx)`.
**On gRPC the declaration is the only trigger.** The gRPC client never marks
delivery evidence — grpc-go reports a failed dial and a connection lost
mid-call as the same status — so without `retry.Idempotent(ctx)` the default
predicate retries no gRPC error at all and `retry.Client` is a no-op. The HTTP
client does mark node-selection failures, dial failures, and failed WebSocket
handshakes, so those retry without the declaration.

`ratelimit`, `retry`, and `timeout` accept `WithRules(...)` for dynamic
governance driven by config; see
[docs/design/dynamic-governance.md](../design/dynamic-governance.md).

## Composing by hand

Only for building your own runtime; generated wrappers already do this.

```go
chained := middleware.ChainUnary(a, b, c)          // no validation
handler, err := middleware.ComposeUnary(next, a, b) // validates, returns error
```

`ChainStream` and `ComposeStream` are the stream equivalents.

## Never write these

| Wrong | Right |
| --- | --- |
| Per-method middleware in a server option | The generated plan: `WrapXxxGRPCServer(srv, XxxMiddleware{Methods: ...})` |
| `middleware.Middleware` / `middleware.Handler` | `UnaryMiddleware` / `UnaryHandler` |
| A `StreamMiddleware` assumed to run per message | It wraps the whole lifecycle; decorate `ServerStream` |
| `stream.SetContext(ctx)` | Decorate `ServerStream` and override `Context()` |
| Ignoring the error from `Wrap...` | Nil middleware is caught only there |
| Mutating a plan after `Wrap...` | Wrappers snapshot; build the plan first |
| Adding middleware to a running server | Composition is construction-time only, on every layer |
| Parsing `tr.Operation()` to route | Treat it as opaque; dispatch on `Kind()` |
