# HTTP Middleware Dispatch Benchmarks: July 23, 2026

## Environment

- Implementation: generated middleware wrappers with construction-time
  `middleware.ComposeUnary`
- Go: `go1.27rc2`
- OS/architecture: macOS 27.0, `darwin/arm64`
- CPU: Apple M5 Pro
- `GOMAXPROCS`: 1
- Samples: 5, run sequentially

The benchmark isolates middleware handler composition. Each middleware is a
real pass-through wrapper that allocates a closure when composed and calls its
next handler on each request. The measurements do not remove or claim to
improve work performed inside application middleware.

## Commands

```shell
GOMAXPROCS=1 go test ./transport/http -run '^$' \
  -bench '^BenchmarkMiddlewareDispatch$' \
  -benchmem -count=5
```

## Results

| Middleware count | Request-time composition | Precomposed | Change | Bytes | Allocations |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 2.523 ns | 2.522 ns | no material change | 0 B -> 0 B | 0 -> 0 |
| 1 | 15.75 ns | 2.587 ns | -83.57% | 16 B -> 0 B | 1 -> 0 |
| 3 | 45.21 ns | 5.183 ns | -88.54% | 48 B -> 0 B | 3 -> 0 |

Values are medians. The three-layer precomposed case was repeated with a
three-second benchmark time and measured 5.129-5.480 ns/op across five samples.

## Interpretation

Generated HTTP and gRPC wrappers now snapshot and compose each unary middleware
chain once through `middleware.ComposeUnary`. Middleware execution still occurs
once per request and retains service-before-method order, request message,
context, response, error, and panic semantics.

This removes a small framework-only dispatch cost. It matters most for very
light handlers and should not be interpreted as an equivalent end-to-end
throughput improvement when serialization, telemetry, network I/O, or business
work dominates the request.

Stream middleware is also composed during generated wrapper construction, but
it uses a separate lifecycle contract and is not represented by this unary
benchmark. HTTP filters, gRPC interceptors, and client middleware are also out
of scope.
