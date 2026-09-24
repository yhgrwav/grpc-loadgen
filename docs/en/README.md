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

The method name is the full one: `package.Service/Method`. The tool gets the method's description
from the service itself through gRPC server reflection, so there is no `.proto` to put anywhere.

| Field | What it does |
|---|---|
| `name` | Optional. What to call the run in the header. Left out — the service name if every method belongs to one service, otherwise the config file name |
| `app.target` | The service address: `ip` and `port` |
| `app.tls` | TLS. Left out — on. `false` — an unencrypted connection |
| `app.ca` | A PEM file of certificates to verify the service with instead of the system ones. Needs TLS |
| `app.cert`, `app.key` | A client certificate and its key in PEM, for a service with mTLS. Only together, needs TLS |
| `app.server_name` | The name to check the service's certificate against when it does not name the address in `target`. Needs TLS |
| `app.metadata` | Headers of every call: `authorization`, `x-api-key` and so on. `${NAME}` is taken from an environment variable |
| `app.max_response_size` | The largest reply a call accepts: `16MiB`, `512KB`. The unit is required (`MB` = 10⁶ bytes, `MiB` = 2²⁰), below 2 GiB. Left out — 4 MiB, as in gRPC. A larger reply is a rejected request, not an overloaded target |
| `load.warmup` | The first N seconds stay out of the percentiles and of `sent`: cold caches spoil them. Warm-up calls do reach the target; the report prints them on a line `warm-up N sent (M failed), excluded from stats` — `sent` plus that line is every call that went out. The target got all of them except those counted as unreachable; `cut off` ones may not have reached it either. Counts toward `duration`, shorter than any call |
| `load.calls[].method` | The full method name |
| `load.calls[].rps` | Requests per second for this method |
| `load.calls[].duration` | How long to load it: `30s`, `5m`, `1h` |
| `load.calls[].timeout` | How long to wait for a reply. Left out — `2s`. Zero does not turn it off, it is an error |
| `load.calls[].data` | The request body, see below. Left out — an empty message |

The config is read strictly: a typo in a field name is an error with the line number, a wrong
value is an error with the call's number and method, not a run with an empty load. `rps` is a
whole number: `10.5` is refused, not silently rounded to ten. `warmup` must be shorter than every
call, or not a single measured request would be left of that call.

**Timeout and the in-flight cap.** If the service hangs, each method holds `rps × timeout`
requests in flight until the timeout fires, plus a margin: one request at the window's edge and
`rps × 100 ms` for the generator freeing a slot a little after the deadline. The sum over methods
must not exceed `-max-in-flight` (5000 by default). This is checked before the start, and the
error names both ways out: which timeout would fit and which cap is needed. So a hung service does
not hit the cap: the run reaches its end, and the report says how many calls got no reply within
the timeout and at which planned call the target answered for the last time. If the cap does run
out, slots were held past their deadline by more than 100 ms — that is the generator (not enough
CPU) or the sender, and the report declares the run invalid. Next to it the report prints how many
slots were held past their deadline at that moment. How many of them went past the margin the
tool does not count: one is enough for the verdict.

### Access to the service

```yaml
app:
  target:
    ip: 10.0.3.17
    port: 443
  ca: certs/ca.pem            # paths are relative to the config file
  cert: certs/client.pem
  key: certs/client.key
  server_name: payments.internal
  metadata:
    authorization: Bearer ${PAYMENTS_TOKEN}
    x-api-key: ${PAYMENTS_KEY}
```

**Secrets.** `${NAME}` is filled from an environment variable, inside a value too:
`Bearer ${TOKEN}`. A variable that is not set or set empty is an error before the start, naming
it. Otherwise `Bearer ` would go out without a token, and the run would show 100% failures as the
target's fault. To write `${` literally, double the dollar: `$${`. A single `$` stays as written.
Substitution works only in `app.metadata`: in `data`, `target` and every other field `${NAME}`
goes out as written. There is no flag for headers, so the token never lands in `ps` or in the shell history. The tool
prints header values nowhere: not in the report, not during the run, not in errors.

**Headers.** Names are lowercased, as HTTP/2 carries them anyway. A config error before the
start: a key starting with `grpc-` (reserved by gRPC), pseudo-headers such as `:path`, a key
ending in `-bin` (binary headers are not supported yet), a value outside printable ASCII. The same
headers and certificate go into the method check before the start. If the target answered it
with `Unauthenticated` while headers are set, the run does not start: "target rejected
credentials" — with a wrong token it would have shown a config error as the target's result.
`PermissionDenied` does not stop the run: the token was accepted and may work for calls while not
letting it into reflection; the methods are marked unchecked. `Unauthenticated` without headers
does not stop it either, and a note says: "target requires credentials; app.metadata is not set".

**Certificates.** `ca` replaces the system roots rather than adding to them. Checking the
target's certificate cannot be turned off by anything. `server_name` changes only the name the
certificate is checked against and that goes into SNI; `:authority` stays the address from
`target`, so a target that routes by it sees the same request. A password-protected key is not
supported: decrypt it beforehand.

**One connection, the target's service config is ignored.** The generator holds one connection to
one address. If DNS returns several addresses, only one backend is loaded: there is no balancing
between them. The report does not show this: it does not know how many addresses DNS returned.
The service config the target hands out through its resolver is not applied: it could open a
connection to every address, shorten our timeout, hold calls on a failed connection instead of
failing them, or cap the reply size — that is, change what is measured. Calls are not retried
either.

**Errors before the start.** Everything that can be known before the run means exit code 1 and not
a single call to the target: a file not found or not PEM, a certificate that does not match its
key, the target's certificate expired or not naming the address (hint: `server_name`), the target
closing the connection right after the handshake (it did not accept the client certificate), TLS
on while the target has none, or the other way round.

### Request body

```yaml
    - method: wallet.v1.WalletService/GetBalance
      rps: 800
      duration: 1m
      data:
        wallet_id: "w-123"
        currency: USD                         # enum — by name
        filter:
          since: "2026-01-01T00:00:00Z"       # google.protobuf.Timestamp
```

`data` is plain YAML shaped like the message. The tool takes the schema from the service through
server reflection; no `.proto` is needed. The body is built once before the start, and any error
in it — an unknown field, a wrong type, a method that does not exist — shows at once, with the
method and field names, before the first request. No `data` — an empty message goes out, and such
a method needs no reflection.

**One body per method.** Every call of a method goes out with the same `data`. For a write with an
idempotency key this means the first call creates the record and every later one takes the repeat
path: the target returns the answer it already stored. That path is what gets measured, not
creating the record. Different data for each call is planned.

Value rules are the standard JSON rules for protobuf (`protojson`):

- a field name as in the `.proto` (`wallet_id`) or in its JSON form (`walletId`);
- an enum by name; `Timestamp`, `Duration` as a string in their format;
- `int64` and `uint64` arrive exactly, above 2^53 too (snowflake IDs, amounts in minor units): the
  number never passes through a float;
- a number with a leading zero (`0123`, `007`) is a config error: YAML would read it as octal.
  Need the zero — write it as a string, `"0123"`;
- **`bytes` as a base64 string.** `signature: abcd` is not the four bytes `abcd` but three other
  bytes: `abcd` is itself valid base64, and there will be no error. The four bytes `abcd` are
  written as `signature: YWJjZA==`.

Every method in the config is checked against the service before the start: a typo in the name or
a method the service does not have is an error before the first request, not a run in which every
call fails with `Unimplemented`.

If reflection is off on the service, a method with `data` will not start — the error says so
plainly. A method without `data` works without reflection too: there is nothing to check it with,
and a warning says so — before the run on stderr and once more as a line in the report, because
the full-screen view wipes the first. The warning says why the check was not possible: reflection
is off, or it refused or did not answer at all — these are different things.

## Run

```console
$ leettest -c leettest.yaml
```

Before the start the tool connects to the service. An unreachable address is an error at once,
with the address and the reason, without a run and without a report.

**Where to run.** Put the generator next to the target: in the same network, on the same machine
or in the same cluster. Everything between them goes into the latency and looks like the target's
time. On our stand at 1000 RPS, the generator on the host through Docker Desktop port forwarding
showed p99 38 ms, and inside the Docker network 2 ms: the same target, and the first time we
measured Docker's network, not it.

| Flag | What it does |
|---|---|
| `-c` | Path to the config |
| `-connect-timeout` | How long to wait for a service that accepted the connection but stays silent. Default `10s`. A refused connection and a wrong address are not waited for |
| `-max-in-flight` | Cap on requests waiting for a reply. Default `5000` |
| `-fake` | Load a built-in stub instead of the service from the config — to look at the tool without a service. The report is marked `fake target` |
| `-fake-delay`, `-fake-jitter`, `-fake-fail-ratio` | The stub's behavior. Only together with `-fake` |
| `-version` | Print the version and exit. A build from source prints the commit |

In a terminal the run goes full-screen: RPS, requests in flight, errors and percentiles in real
time, `q` to stop. Without a terminal (in CI, with redirected output) — a progress line once a
second:

```
32.0s  sent 25600  rps 800  in-flight 47  failed 51  not-sent 0  p99 43ms
```

At the end — a report per method: sent, failed, `sent/s`, p50/p90/p95/p99. `sent/s` is the
requests sent divided by the sending time: warm-up and waiting for the last replies after the end
of the plan are not in it, so a target hanging at the end of the run does not lower this number.
Requests cut off by the timeout are not replaced by a number: if they could take the percentile's
place, it is printed as a lower bound, `>2.0s`. Requests that never reached the service count as
failures but stay out of the percentiles. A request that went out but got no status — the other
end reset the stream or dropped the connection — is counted separately, as `cut off`: the target
or a proxy may have processed it, which for a write is a reason to check for duplicates.

Failures are split: the `error status` row is how many times an error status came back
(`RESOURCE_EXHAUSTED`, `UNAVAILABLE`, `INTERNAL` and others) and how long it took. The status may
have come not from the target itself but from a proxy in front of it: nginx without a live backend
answers `UNAVAILABLE`, and the client cannot tell one from the other. The `rejected` row is how many
times a call would have failed at any rate: a wrong request (no such method, a bad argument, a
body that does not match the schema) or a message that did not fit — a reply rejected whole by the
client's 4 MB limit (nothing of it arrives), or a request the target or a proxy in front of it
found too large (their limit is unknown to us). The latter does not depend on the load: such a
call fails at any RPS. If every measured call of a method is rejected, the run is declared invalid
and the verdict names that method: with one typo in three methods the run's share would be 33%,
and no verdict at all.

Below the report, each method's failed calls are broken down by gRPC code, commonest first, on two
lines. "sent by the target" — the status came over the wire: from the target or from a proxy in
front of it, the client cannot tell which. "set by the client, no status came back" — there was no
status, the client set the code itself: nobody answered, the other end reset the stream, our
deadline ran out. The same code can appear on both lines. `DeadlineExceeded` from the target is
usually our own deadline: it goes to the target in the `grpc-timeout` header, and the target may
end the call before our timer does. The category says whose fault it is, the code says what to
look for in the target's logs.

The load goes over one connection. The report prints how many times it reconnected and which
concurrent stream limit (`MAX_CONCURRENT_STREAMS`) the target announced, or that it announced none.
A call counts from its planned moment, and everything it waited on the client side is in its
latency: the generator running late, waiting for a ready connection, waiting for a free stream. If
without these waits the printed p99 of at least one method changes, or some calls never went out,
the report gives a verdict and names the cause most common in the p99 tail: the generator ran
late, there was no connection to the target, the streams ran out. In the last case the target's
capacity above "limit × connections" calls in flight is not measured. For each affected method it
prints p99 without the client-side waits — the time the call spent at the target. If a call waited
for the resolver first, the caller's interceptors land in the connection wait too, so this number
may come out slightly low. Calls that never went out are split by reason: waited for a stream,
waited for the connection, generator late.

The report goes to stdout, progress and errors to stderr.

**Stopping** (`q` in the full-screen view, Ctrl+C without it):

- the first press — no new requests go out, requests in flight run to their timeout and land in
  the report as usual;
- the second — requests in flight are cut off and counted separately as cut off: not a service
  failure but a lower bound of the time; the report is printed;
- the third — exit at once, without a report.

In the full-screen view the top line walks through these steps: while requests in flight finish,
it shows how many are left and that `q` once more cuts them off; after the cut-off — that `q` once
more exits without a report.

SIGTERM (`docker stop`, Kubernetes, a cancelled CI job) cuts off the requests in flight at once
and prints the report, skipping the gentle stop: the orchestrator kills the process a few seconds
later, and the report must make it out. If a cut-off is already under way, SIGTERM does not
interrupt it.

A stopped run is marked incomplete in the report: its numbers are honest but cover less than was
planned.

**Exit codes.** There are no "holds / does not hold" thresholds yet, so a run that reaches its end
returns `0` however the target answered: **`0` does not mean "the service is healthy"**. A run
where 100% of calls got no reply also returns `0` — and the report says so plainly. A threshold
and a non-zero code for it come in stage 2. The codes are ranked, not summed: an invalid run (`2`)
outranks an incomplete one (`3`), and `130` and `143` are the exit without a report, so a stop
that did print a report is always `3`.

| Code | What happened |
|---|---|
| `0` | The plan ran, the report is complete |
| `1` | The run did not happen: flags, config, connection. No report |
| `2` | The run is invalid: it hit the in-flight cap, or the target rejected every call of some method as a wrong request. There is a report, but its numbers are not about the target |
| `3` | The run stopped before its plan. There is a report, and it covers only what got through |
| `130` | Aborted without a report after Ctrl+C |
| `143` | Aborted without a report after SIGTERM |

## Not yet

Ramp-up from zero to the target RPS, pass/fail thresholds and a JSON report for CI, export to
Prometheus.

## Going deeper

| Question | |
|---|---|
| What problem does LeetTest solve? | [Read](problem.md) |
| Why this tool? | [Read](why.md) |
| Which load-testing problems does it fix? | [Read](pitfalls.md) |
| What is call chaining and why does it matter? | [Read](chaining.md) |
| What does commercial use look like? | [Read](commercial.md) |
| Where do I ask a question or leave feedback? | [Read](feedback.md) |

**[Comparison with ghz on one stand →](../compare-ghz.md)** — tables and the command to reproduce.

## Contributing

Issues and discussions are welcome. Pull requests are accepted after signing the [CLA](../../CLA.md)
— one line as a comment in the PR, checked automatically. How to start — in
[CONTRIBUTING.md](../../CONTRIBUTING.md).

The CLA is a license, not a transfer of rights: the copyright stays with you.

## License

Apache License 2.0 — see [LICENSE](../../LICENSE).
