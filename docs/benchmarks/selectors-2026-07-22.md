# Selector Benchmarks: July 22, 2026

## Environment

- Baseline: `3f29df21` (Forge before selector PRs)
- Optimized algorithms: WRR `42b647be`, P2C `f1246466`
- Benchmark harness: `4fa25d1b`
- Go: `go1.26.4`
- OS/architecture: `darwin/arm64`
- CPU: Apple M5 Pro
- Samples: 10
- Sample duration: 200ms
- Comparison: `benchstat` from `golang.org/x/perf`

The same benchmark files from `4fa25d1b` were applied to a detached baseline
worktree. Baseline and optimized samples ran sequentially to avoid CPU contention.

## Commands

```shell
GOMAXPROCS=8 go test ./selector/wrr -run '^$' \
  -bench '^(BenchmarkPickWorkloads|BenchmarkPickParallel)$' \
  -benchmem -benchtime=200ms -count=10

go test ./selector/p2c -run '^$' -bench '^BenchmarkSelect' \
  -benchmem -benchtime=200ms -count=10 -cpu=1,2,4,8,16

benchstat before.txt after.txt
```

## WRR

The add-only, removal, and replacement measurements are complete state-transition
cycles. Stable and parallel measurements report individual picks.

| Workload | Nodes | Before | After | Change |
| --- | ---: | ---: | ---: | ---: |
| Stable | 1 | 47.19 ns | 27.56 ns | -41.59% |
| Stable | 5 | 159.50 ns | 95.60 ns | -40.06% |
| Stable | 10 | 382.6 ns | 198.4 ns | -48.14% |
| Stable | 100 | 3.580 us | 1.803 us | -49.64% |
| Add-only cycle | 100 | 223.67 us | 93.68 us | -58.12% |
| Removal cycle | 100 | 8.870 us | 7.458 us | -15.93% |
| Replacement cycle | 5 | 501.8 ns | 625.1 ns | +24.57% |
| Replacement cycle | 10 | 1.290 us | 1.290 us | no significant change |
| Replacement cycle | 100 | 13.24 us | 13.16 us | no significant change |
| Parallel pick | 10 | 484.0 ns | 263.7 ns | -45.52% |

Stable picks at 10 and 100 nodes changed from 3 allocations per operation to
zero. Parallel picks changed from 456 B and 3 allocations to zero. The isolated
five-node full-replacement regression is accepted because discovery replacement
is a control-plane event, while stable `Pick` is the per-request hot path. It
remains a target for future churn-specific profiling.

## P2C

| Benchmark | GOMAXPROCS | Before | After | Change |
| --- | ---: | ---: | ---: | ---: |
| Parallel | 1 | 196.6 ns | 198.4 ns | +0.92% |
| Parallel | 2 | 195.7 ns | 161.3 ns | -17.55% |
| Parallel | 4 | 169.2 ns | 104.1 ns | -38.49% |
| Parallel | 8 | 219.55 ns | 99.22 ns | -54.81% |
| Parallel | 16 | 279.25 ns | 87.79 ns | -68.56% |

Serial results ranged from -1.07% to +3.65% without a consistent trend. All P2C
cases remained at 72 B and 2 allocations per operation. The optimization is a
concurrency scaling improvement and does not materially improve single-threaded
selection.
