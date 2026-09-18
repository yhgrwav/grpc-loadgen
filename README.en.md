# grpc-loadgen

[Русский](README.md) · **English** · [Deutsch](README.de.md) · [中文](README.zh-CN.md)

Declarative load testing for gRPC services.

> **Status: early WIP.** The repository is a skeleton — the engine is not implemented yet.
> Nothing below works today; the CLI and config shapes are here to pin down the UX.

## The problem

Existing tools each miss something when you need to load-test a real service:

| | multiple methods with **different** RPS in one run | dependencies between calls | latency without coordinated omission | no codegen / no `.proto` needed |
|---|---|---|---|---|
| `ghz` | no — one method per run | no | partial | yes (reflection) |
| `k6` (grpc) | scripted, closed model by default | manual | closed model hides stalls | needs `.proto` |
| jmeter-grpc-request | clumsy | manual | no | needs `.proto` |
| **grpc-loadgen** | yes | planned (stage 1) | yes, by design | yes (reflection) |

Concretely: you want `GetBalance` at 800 RPS, `Transfer` at 50 RPS and `CreateWallet` at 5 RPS,
all at once, against the same service, in one report.

## Design decisions

**Open model.** A target rate is a schedule, not a concurrency level: requests go out on the
clock whether or not earlier ones came back. A closed model (N virtual users, each awaiting a
reply) throttles itself exactly when the service starts degrading — which is the moment you
were trying to measure.

**Latency is measured from `scheduledAt`.** At a 1000 RPS target (one request per ms), suppose
the service freezes for one second. 1000 requests were due during that second.
Measured from actual send time, they all leave after the freeze, take 5ms each, and the report
says `p99 = 5ms` — the outage disappears. Measured from the time each request was *scheduled*,
the one due at `t=0` left at `t=1000ms` and answered at `t=1005ms`: `1005ms`. Same run, two
reports; only one of them is true. This is in the core from the first commit, not bolted on.

**Explicit in-flight cap.** Under an open model a stalled target accumulates hanging requests
until the generator dies before the service under test does. Exceeding the cap is reported as a
run error — never a silent reduction of the offered rate, which would quietly invalidate results.

**Mergeable metrics.** Recording happens on the hot path: HDR histograms and sharded counters,
not a mutex around a slice. Percentiles are computed from merged histograms, never averaged —
averaging p99s is meaningless, and distributed runs (stage 3) must merge distributions.

**Descriptors via gRPC server reflection.** No codegen, no `.proto` to place anywhere. Work is
at the protobuf wire level, so the service's implementation language is irrelevant.
`.proto` parsing (in-process, no external `protoc`) is a stage-2 fallback for services with
reflection disabled.

**Unary RPC only in the MVP.** Streaming is on the roadmap.

## Intended usage

```yaml
# loadgen.yaml
target:
  address: localhost:50051
  plaintext: true

defaults:
  duration: 60s
  max_in_flight: 5000

calls:
  - method: wallet.v1.WalletService/GetBalance
    rps: 800
    payload:
      wallet_id: "w-0001"

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    payload:
      from: "w-0001"
      to: "w-0002"
      amount: 100

thresholds:          # optional; breach -> exit code 1
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

```console
$ grpc-loadgen run -c loadgen.yaml
```

Thresholds are what makes it usable in CI: report and exit 0 normally, report and exit 1 on a
breach, so the pipeline fails.

## Roadmap

- **Stage 0 (MVP)** — load engine, no inter-call dependencies: reflection, per-method `{method, rps, duration}`, static payloads, percentile report, optional thresholds.
- **Stage 1** — dependency graph between calls: a pool of live entities (create wallets, reuse their ids in transfers), producer/consumer between request streams, defined backpressure when the pool drains.
- **Stage 2** — `.proto` as a descriptor source, streaming RPCs, custom protobuf options for automatic linking, containerization.
- **Stage 3** — control plane: distributed runs, histogram aggregation across machines, worker start synchronization, dashboard. Separate, closed repository.

## Layout

```
cmd/grpc-loadgen/   CLI entry point
pkg/engine/         scheduler, rate limiting, in-flight cap, worker pool
pkg/descriptor/     method descriptor resolution (reflection)
pkg/metrics/        histograms, sharded counters, percentiles
pkg/config/         config parsing and validation
internal/           private helpers
```

The library core knows nothing about YAML, the CLI or any future control plane. Delivery
mechanisms depend on the core; the core depends on none of them.

## Contributing

**Issues and discussions are welcome. Pull requests are not accepted yet** — a CLA bot is not in
place, and code merged without one cannot be relicensed later. Please open an issue instead.

Dependencies must be permissively licensed (Apache-2.0 / MIT / BSD / ISC). CI fails on copyleft.

## License

Apache License 2.0 — see [LICENSE](LICENSE).
