# HTTP Routing Benchmarks: July 22, 2026

## Environment

- Gorilla baseline: `668db92c2c001e9552594ba5a8aede8456af6d7e`
- ServeMux implementation: `ad04be7b0a10f8d2a7fa57677ffe05fdb2ae06c8`
- Go: `go1.26.4`
- OS/architecture: macOS 27.0, `darwin/arm64`
- CPU: Apple M5 Pro
- `GOMAXPROCS`: 8
- Samples: 10
- Sample duration: Go benchmark default, one second
- Comparison: `benchstat` from `golang.org/x/perf` at
  `82a0b07e230d`

The Gorilla and ServeMux samples ran sequentially in isolated worktrees. Both
harnesses register identical method, path, and handler combinations directly on
their internal routing adapters. The final route is selected for hits so the
comparison includes Gorilla's registration-order scan. Each parallel worker owns
its request and response writer; request objects are never shared across workers.

The module path changed at the fork, so only the `pkg:` label in copies of the
raw output was normalized before running `benchstat`. Benchmark names and
measurements were unchanged.

## Commands

```shell
GOMAXPROCS=8 go test ./transport/http -run '^$' \
  -bench '^BenchmarkRouteMux$' -benchmem -count=10

benchstat gorilla.txt servemux.txt
```

## Serial Dispatch

The parameter case matches a variable route without reading its value. The
parameter-vars case also constructs the public `url.Values` representation used
by request binding.

| Workload | Routes | Gorilla | ServeMux | Time | B/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Static hit | 1 | 618.05 ns | 75.61 ns | -87.77% | 848 -> 0 | 7 -> 0 |
| Static hit | 100 | 5.440 us | 85.61 ns | -98.43% | 848 -> 0 | 7 -> 0 |
| Static hit | 1,000 | 30.219 us | 86.42 ns | -99.71% | 848 -> 0 | 7 -> 0 |
| Parameter hit | 1 | 908.6 ns | 306.1 ns | -66.31% | 1,152 -> 728 | 8 -> 6 |
| Parameter hit | 100 | 3.600 us | 329.4 ns | -90.85% | 1,152 -> 728 | 8 -> 6 |
| Parameter hit | 1,000 | 32.137 us | 391.8 ns | -98.78% | 1,152 -> 728 | 8 -> 6 |
| Parameter read | 1 | 774.6 ns | 457.0 ns | -41.00% | 1,152 -> 1,144 | 8 -> 9 |
| Parameter read | 100 | 3.214 us | 476.8 ns | -85.16% | 1,152 -> 1,144 | 8 -> 9 |
| Parameter read | 1,000 | 32.324 us | 567.3 ns | -98.24% | 1,152 -> 1,144 | 8 -> 9 |
| Miss | 1 | 297.9 ns | 456.2 ns | +53.12% | 96 -> 208 | 4 -> 12 |
| Miss | 100 | 760.0 ns | 462.2 ns | -39.18% | 96 -> 208 | 4 -> 12 |
| Miss | 1,000 | 7.799 us | 466.3 ns | -94.02% | 96 -> 208 | 4 -> 12 |

All time comparisons report `p=0.000`, `n=10`. Static ServeMux dispatch remains
allocation-free. Reading public path variables adds one allocation compared with
Gorilla even though it is 41.00% to 98.24% faster in these measurements. The
standard-library 404 path has a fixed allocation cost and regresses the one-route
case; it overtakes Gorilla as the route table grows.

## Parallel Dispatch

| Workload | Routes | Gorilla | ServeMux | Change |
| --- | ---: | ---: | ---: | ---: |
| Static hit | 1 | 434.7 ns | 122.9 ns | -71.74% |
| Static hit | 100 | 1.110 us | 128.8 ns | -88.39% |
| Static hit | 1,000 | 7.560 us | 123.2 ns | -98.37% |
| Parameter hit | 1 | 541.8 ns | 310.2 ns | -42.74% |
| Parameter hit | 100 | 1.519 us | 323.8 ns | -78.68% |
| Parameter hit | 1,000 | 7.569 us | 287.8 ns | -96.20% |
| Parameter read | 1 | 536.9 ns | 429.4 ns | -20.02% |
| Parameter read | 100 | 1.572 us | 438.3 ns | -72.11% |
| Parameter read | 1,000 | 8.196 us | 438.7 ns | -94.65% |
| Miss | 1 | 110.6 ns | 289.9 ns | +162.07% |
| Miss | 100 | 187.8 ns | 285.1 ns | +51.81% |
| Miss | 1,000 | 1.688 us | 298.9 ns | -82.28% |

All time comparisons report `p=0.000`, `n=10`. Parallel allocation direction
matches the serial cases; small B/op differences in the Gorilla results come
from amortizing per-worker benchmark setup.

## Decision

The migration is accepted. It materially improves successful dispatch, removes
route-count scaling from the measured hot path, eliminates Gorilla mux as a
dependency, and preserves the required Google AIP behavior through the internal
adapter. The fixed 404 cost and the extra allocation when exporting path
variables are recorded trade-offs, not hidden regressions.

This is an internal router-dispatch benchmark. It does not claim the same
percentage improvement for end-to-end HTTP or RPC latency, where codecs,
middleware, application work, and network I/O normally dominate.
