# HTTP Middleware Dispatch Benchmarks: July 23, 2026

## Environment

- Baseline: `fb1b3b0d`
- Optimized implementation: `e71e2767`
- Go: `go1.27rc2`
- OS/architecture: macOS 27.0, `darwin/arm64`
- CPU: Apple M5 Pro
- `GOMAXPROCS`: 1
- Samples: 10, run sequentially
- Comparison: `benchstat` from `golang.org/x/perf`

The benchmark isolates operation matching and middleware handler composition.
Each middleware is a no-op wrapper, so the measurements do not remove or claim
to improve work performed inside middleware on every request.

The same benchmark was applied to a detached baseline worktree. Its
`precomposed` benchmark name identifies the workload used for comparison; the
baseline implementation still called `Context.Middleware` and rebuilt the
matched chain on every iteration.

## Commands

```shell
GOMAXPROCS=1 go test ./transport/http -run '^$' \
  -bench '^BenchmarkMiddlewareDispatch/precomposed' \
  -benchmem -count=10

benchstat middleware-before.txt middleware-after.txt
```

## Results

| Middleware count | Before | After | Change | Bytes | Allocations |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 0 | 7.647 ns | 2.622 ns | -65.71% | 0 B -> 0 B | 0 -> 0 |
| 1 | 21.135 ns | 2.623 ns | -87.59% | 8 B -> 0 B | 1 -> 0 |
| 3 | 31.850 ns | 2.634 ns | -91.73% | 24 B -> 0 B | 1 -> 0 |

All timing differences are statistically significant at `p=0.000`, with ten
samples per revision. The geometric mean changed from 17.27 ns to 2.626 ns
(`-84.79%`).

## Interpretation

Generated unary HTTP handlers now select and compose their middleware chain
once through `Server.WrapMiddleware`. Middleware execution still occurs once
per request and retains its existing order, request message, context, response,
and error semantics.

This removes a small framework-only dispatch cost. It matters most for very
light handlers and should not be interpreted as an equivalent end-to-end
throughput improvement when serialization, telemetry, network I/O, or business
work dominates the request.

Streaming handlers and hand-written `Context.Middleware` calls retain dynamic
terminal-handler composition. They share the frozen middleware configuration
but are not represented by the optimized result above.
