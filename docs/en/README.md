<div align="center">

# grpc-loadgen

**gRPC load testing that doesn't lie.**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

</div>

---

> **Early WIP.** The engine is under construction, there is nothing to run yet. Everything below
> is the interface we are building toward.

One run. As many methods as you like, each at its own RPS. No `.proto`, no codegen, no scripts.

The project rests on three priorities, in this order: **measurement correctness** — the numbers
match what actually happened, including while the service degrades; **usability** — the config,
the errors and the report are built for a person; **speed** — the generator never becomes the
bottleneck.

## Install

```console
$ go install github.com/yhgrwav/grpc-loadgen/cmd/grpc-loadgen@latest
```

## Config

```yaml
app:
  target:
    ip: localhost
    port: 50051
  tls: false

load:
  warmup: 5s
  calls:
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m

    - method: wallet.v1.WalletService/Transfer
      rps: 50
      duration: 1m
```

Method names are fully qualified: `package.Service/Method`. The tool asks the service itself for
the method definition over gRPC server reflection, so there is no `.proto` to place anywhere.

| Field | What it does |
|---|---|
| `app.target` | Service address: `ip` and `port` |
| `app.tls` | TLS. Omitted means enabled; `false` connects without encryption |
| `load.warmup` | The first N seconds stay out of the report — cold caches skew percentiles |
| `load.calls[].method` | Fully qualified method name |
| `load.calls[].rps` | Requests per second for this method |
| `load.calls[].duration` | How long to keep it up: `30s`, `5m`, `1h` |

## Run

```console
$ grpc-loadgen run -c loadgen.yaml
```

While the run is going you can see what is happening:

```
running  32s/60s │ sent 25 600 │ in-flight 47 │ err 0.2% │ p99 43ms
```

At the end: throughput, percentiles and an error breakdown, per method.

## CI

Add thresholds and the run decides whether the build passes:

```yaml
thresholds:
  - metric: p99
    less_than: 200ms
  - metric: error_rate
    less_than: 0.01
```

A breached threshold prints the report and exits with code `1`, failing the pipeline. No
thresholds means exit `0`.

The report goes to stdout, progress and errors to stderr, so
`grpc-loadgen run -c loadgen.yaml > report.json` gives you a clean file.

## Going deeper

| Question | |
|---|---|
| What problem does grpc-loadgen solve? | [Read](problem.md) |
| Why this tool? | [Read](why.md) |
| Which load-testing problems does it fix? | [Read](pitfalls.md) |
| What is call chaining and why does it matter? | [Read](chaining.md) |
| What does commercial use look like? | [Read](commercial.md) |
| Where do I ask a question or leave feedback? | [Read](feedback.md) |

## Contributing

Issues and discussions are welcome. Pull requests are merged once the author has signed the
[CLA](../../CLA.md) — one comment on the pull request, checked automatically. See
[CONTRIBUTING.md](../../CONTRIBUTING.md) to get started.

The CLA is a license, not a transfer of ownership: you keep the copyright in your contribution.

## License

Apache License 2.0 — see [LICENSE](../../LICENSE).
