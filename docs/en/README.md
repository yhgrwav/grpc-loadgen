<div align="center">

<h1><img src="../../assets/logo.png" width="360" alt="LeetTest"></h1>

**gRPC load testing that doesn't lie.**

[Русский](../ru/) · [English](../en/) · [Deutsch](../de/) · [中文](../zh-CN/)

[![CI](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/yhgrwav/leettest/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/yhgrwav/leettest.svg)](https://pkg.go.dev/github.com/yhgrwav/leettest)
[![Go version](https://img.shields.io/github/go-mod/go-version/yhgrwav/leettest)](../../go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](../../LICENSE)

</div>

---

> This translation may lag behind the [Russian original](../../README.md).

> **Early stage.** Working now: unary load against a real service, several methods at their own
> RPS in one run, request bodies from the config, a console report. Not yet: ramp-up, pass/fail
> thresholds for CI, a JSON report, metrics export. Everything below describes what already
> works.

The tool answers the question people bring to a load test: **at what load does the service stop
coping, and where is the bottleneck.** To get there it applies load that looks like production —
several methods at once, each at its own RPS — and measures so that the numbers don't lie while
the service degrades. No `.proto`, no codegen, no scripts.

The project rests on three priorities, in this order.

**Measurement correctness** — the numbers match what actually happened, including while the
service degrades.

**Usability.** This category takes for granted that a tool built for engineers is allowed to be
unpleasant to use. I disagree: the interface is one of the product's main advantages, and an
unclear error message is as much a defect as a wrong number.

**Speed** — the generator never becomes the bottleneck.

## Install

```console
$ go install github.com/yhgrwav/leettest/cmd/leettest@latest
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

The method name is fully qualified: `package.Service/Method`. The tool asks the service itself
for the method description through gRPC server reflection, so there is no `.proto` to put
anywhere.

| Field | What it does |
|---|---|
| `name` | Optional. What to call the run in the header. If absent — the service name when all methods belong to one, otherwise the config file name |
| `app.target` | Service address: `ip` and `port` |
| `app.tls` | TLS. On if absent. `false` connects without encryption |
| `load.warmup` | The first N seconds stay out of the percentiles: cold caches skew them |
| `load.calls[].method` | Fully qualified method name |
| `load.calls[].rps` | Requests per second for this method |
| `load.calls[].duration` | How long to load it: `30s`, `5m`, `1h` |
| `load.calls[].timeout` | How long to wait for a reply. `2s` if absent. Zero does not disable it, it is an error |
| `load.calls[].data` | Request body, see below. Empty message if absent |

The config is read strictly: a typo in a field name or a zero `rps` is an error that says where,
not a run with no load.

**Timeout and the in-flight cap.** When the service hangs, each method keeps `rps × timeout`
requests in flight until the timeout fires. The sum over methods must stay within
`-max-in-flight` (5000 by default), otherwise the cap runs out before the first timeout and the
run fails without ever showing that the service hung. This is checked before the start, and the
error names both ways out: which timeout fits and which cap is needed.

### Request body

```yaml
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
      data:
        wallet_id: "w-123"
        currency: USD                         # enum by name
        filter:
          since: "2026-01-01T00:00:00Z"       # google.protobuf.Timestamp
```

`data` is plain YAML shaped like the message. The tool takes the schema from the service through
server reflection; no `.proto` needed. The body is built once before the start, and any error in
it — unknown field, wrong type, missing method — shows up at once, naming the method and the
field, before the first request. No `data` sends an empty message, and such a method needs no
reflection.

Value rules are the standard JSON rules for protobuf (`protojson`):

- field names as in `.proto` (`wallet_id`) or in JSON form (`walletId`);
- enums by name; `Timestamp` and `Duration` as strings in their format;
- `int64` and `uint64` arrive exactly, including above 2^53 (snowflake IDs, amounts in minor
  units): no number passes through a float;
- a number with a leading zero (`0123`, `007`) is a config error: YAML would read it as octal.
  If you need the zero, write it as a string, `"0123"`;
- **`bytes` as base64 strings.** `signature: abcd` is not the four bytes `abcd` but three other
  bytes: `abcd` is itself valid base64, and there is no error. The four bytes `abcd` are written
  as `signature: YWJjZA==`.

If reflection is off on the service, a method with `data` will not start — the error says so.
A method without `data` works without reflection.

## Run

```console
$ leettest -c leettest.yaml
```

Before the start the tool connects to the service. An unreachable address is an error right
away, with the address and the reason, with no run and no report.

| Flag | What it does |
|---|---|
| `-c` | Path to the config |
| `-connect-timeout` | How long to wait for a service that accepted the connection but stays silent. `10s` by default. A refused connection or a bad address does not wait |
| `-max-in-flight` | Cap on requests waiting for a reply. `5000` by default |
| `-fake` | Load a built-in stub instead of the service from the config — to try the tool without a service. The report is marked `fake target` |
| `-fake-delay`, `-fake-jitter`, `-fake-fail-ratio` | Stub behaviour. Only together with `-fake` |

In a terminal the run is full-screen: RPS, requests in flight, errors and percentiles live, `q`
to stop. Without a terminal (in CI, with redirected output) — a progress line every second:

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  p99 43ms
```

At the end — a report per method: sent, failed, RPS, p50/p90/p95/p99. Requests cut off by the
timeout are not replaced with a number: if they could occupy a percentile's place, it is printed
as a lower bound, `>2.0s`. Requests that never reached the service count as failures but stay out
of the percentiles.

The report goes to stdout, progress and errors to stderr.

## Not yet

Ramp-up from zero to the target RPS, pass/fail thresholds and a JSON report for CI, a breakdown
of failures by code in the report, export to Prometheus.

## Going deeper

| Question | |
|---|---|
| What problem does LeetTest solve? | [Read](problem.md) |
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
