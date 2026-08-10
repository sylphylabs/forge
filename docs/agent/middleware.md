# Middleware

Package `github.com/sylphylabs/forge/middleware`.

**Server middleware is attached through generated code, not a server option.**
There is no `grpc.Middleware(...)` and no `http.Middleware(...)`. This is the
single most common mistake when writing Forge code.

Client middleware *is* a client option: `WithMiddleware(...)`.

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
	forgegrpc.WithEndpoint("discovery:///greeter"),
	forgegrpc.WithMiddleware(tracing.Client(), retryMiddleware),
)

client, err := forgehttp.NewClient(ctx,
	forgehttp.WithEndpoint(endpoint),
	forgehttp.WithMiddleware(tracing.Client()),
)
```

## Built-in middleware

| Package | Constructor | Side |
| --- | --- | --- |
| `middleware/recovery` | `Recovery(opts...)` | server |
| `middleware/logging` | `Server(logger)` / `Client(logger)` | both |
| `middleware/validate` | `Validator(validators...)` | server |
| `middleware/ratelimit` | `Server(opts...)` | server |
| `middleware/timeout` | `Server(opts...)` | server |
| `middleware/metadata` | `Server(opts...)` / `Client(opts...)` | both |
| `middleware/retry` | `Client(opts...) (UnaryMiddleware, error)` | client |
| `middleware/circuitbreaker` | `Client(opts...)` | client |
| `middleware/selector` | `Client(ms...) *Builder` | client |
| `contrib/otel/tracing` | `Server(opts...)` / `Client(opts...)` | both |

`retry.Client` returns an error — it validates its policy at construction.
Retries are opt-in per call for non-idempotent operations:
`ctx = retry.Idempotent(ctx)`.

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
| `grpc.NewServer(grpc.Middleware(m))` | `WrapXxxGRPCServer(srv, XxxMiddleware{Unary: ...})` |
| `http.NewServer(http.Middleware(m))` | `WrapXxxHTTPServer(srv, XxxMiddleware{...})` |
| `middleware.Middleware` / `middleware.Handler` | `UnaryMiddleware` / `UnaryHandler` |
| A `StreamMiddleware` assumed to run per message | It wraps the whole lifecycle; decorate `ServerStream` |
| `stream.SetContext(ctx)` | Decorate `ServerStream` and override `Context()` |
| Ignoring the error from `Wrap...` | Nil middleware is caught only there |
| Mutating a plan after `Wrap...` | Wrappers snapshot; build the plan first |
| Parsing `tr.Operation()` to route | Treat it as opaque; dispatch on `Kind()` |
