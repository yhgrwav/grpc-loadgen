# LeetTest and ghz on the same stand

This page compares LeetTest with [ghz](https://ghz.sh) on a test server with known behavior. The
question is narrow: when the server stalls or slows down, does the report show it?

In short:

- **When the server behaves**, both tools report the same latencies (mode A).
- **When the server freezes for 2 seconds** (mode B1), ghz in its default synchronous mode
  (`-c 10`) reports p99 of 10.8ms. LeetTest reports 1.71s. ghz with `--async` also reports 1.71s.
- **When the server is slower than the load needs** (mode B2), ghz in its default synchronous mode
  (`-c 10`) sends 2980 of the 6000 requested calls. Its latencies are correct for the calls it
  sent, but that is half the requested load.

This is not a bug in ghz. The synchronous mode runs a fixed number of workers. Each worker waits
for its call to finish before starting the next one: a closed model, by design. When the server
stalls, the workers wait too, and calls that should have gone out during the stall are never
sent. So their delay is never measured. This effect is known as *coordinated omission*. With
`--async`, ghz sends on schedule whether or not earlier calls have returned: an open model. That
gives the same numbers as LeetTest. LeetTest has only the open model.

## Setup

The stand is `test/stand` in this repository: a gRPC server answering `grpc.health.v1.Health/Check`
after a set delay. It can freeze for a set time or answer more slowly.

| mode | stand | what it models |
|---|---|---|
| A | `-delay 10ms` | control: a server that keeps up |
| B1 | `-delay 10ms -freeze-at 10s -freeze-for 2s` | a 2-second stall 10s into the run (a GC pause, a lock) |
| B2 | `-delay 100ms` | a server slower than the load needs |

Load in every run:

- 200 calls per second for 30 seconds, which is 6000 calls;
- a 5s timeout;
- one connection.

The ghz flags are:

```
ghz --insecure --call grpc.health.v1.Health/Check -d {} --rps 200 -z 30s -t 5s \
    --duration-stop wait --connections 1 -c 10 [--async]
```

`-c 10` is the ghz default. `--duration-stop wait` lets calls still in flight at 30s finish and be
counted.

Every tool ran 5 times in every mode, and each run got a fresh stand. The run order rotates, so
no tool always runs first. The tables show the median of the 5 runs, with the minimum and maximum
in brackets.

Environment:

- LeetTest at commit `4be4115`;
- ghz v0.121.0 (grpc-go v1.56.3);
- Go 1.26.1;
- Windows 11 (10.0.26100), AMD Ryzen 9 5900X, 32 GB RAM;
- tools and stand on the same machine, over loopback.

## Results

### B1: a 2-second freeze

| tool | sent | failed | p50 | p90 | p95 | p99 | calls over 1s |
|---|---|---|---|---|---|---|---|
| LeetTest | 6000 [6000–6000] | 0 | 10.6ms [10.5–10.6] | 11.2ms [11.1–11.2] | 514ms [510–516] | 1.71s [1.71–1.72] | not reported |
| ghz, sync (`-c 10`) | 5999 [5999–6002] | 0 | 10.2ms [10.1–10.2] | 10.5ms [10.4–10.6] | 10.6ms [10.6–10.7] | 10.8ms [10.8–10.9] | 10 [10–10] |
| ghz, `--async` | 5999 [5999–6000] | 0 | 10.2ms [10.2–10.3] | 10.7ms [10.7–10.7] | 514ms [510–515] | 1.71s [1.71–1.72] | 203 [202–203] |

Over 2 seconds at 200 RPS, about 400 calls were due. With `--async`, ghz counts 203 calls over 1s:
the ones due in the first half of the freeze wait longer than a second. In synchronous mode, 10
calls are over 1s, one per worker. The workers stood still during the freeze. When it ended,
ghz sent the missed calls at once to catch up, and those calls met a server that was already
fast again. Their wait before sending is not part of the latency ghz reports.

The number of calls sent is the same in all three rows, so the count does not reveal the stall.

### B2: a server slower than the load needs

| tool | sent | failed | p50 | p90 | p95 | p99 |
|---|---|---|---|---|---|---|
| LeetTest | 6000 [6000–6000] | 0 | 101ms [101–101] | 101ms [101–101] | 101ms [101–101] | 102ms [102–102] |
| ghz, sync (`-c 10`) | 2980 [2980–2980] | 0 | 100.6ms [100.5–100.6] | 101.1ms [101.1–101.2] | 101.3ms [101.2–101.5] | 102ms [101.8–102.8] |

10 workers × 1 call per 100ms is 100 calls per second, half of the 200 requested. The latencies
are right; the load is not. Reading only the latency columns, this run looks like a server
that handles 200 RPS at 101ms. The server handled 100 RPS.

ghz with `--async`, or with a larger `-c`, sends the full load. This mode shows only what the
default does.

### A: control

| tool | sent | failed | p50 | p90 | p95 | p99 |
|---|---|---|---|---|---|---|
| LeetTest | 6000 [6000–6000] | 0 | 10.6ms [10.6–10.6] | 10.9ms [10.9–11.0] | 11.1ms [11.0–11.1] | 11.3ms [11.2–11.5] |
| ghz, sync (`-c 10`) | 6000 [5999–6001] | 0 | 10.2ms [10.2–10.3] | 10.5ms [10.5–10.5] | 10.5ms [10.5–10.5] | 10.7ms [10.7–10.8] |
| ghz, `--async` | 6001 [5999–6002] | 0 | 10.2ms [10.2–10.2] | 10.5ms [10.5–10.5] | 10.5ms [10.5–10.6] | 10.7ms [10.7–10.8] |

All three agree within about 0.5ms. LeetTest reads slightly higher because it measures from the
moment a call was scheduled, while ghz measures from the moment the call starts. The difference
is the client's own delay before sending. It is kept on purpose: the user of a service waits
that time too.

## Reproduce

Install ghz, then run from the repository root:

```
go run ./test/compare-ghz -ghz /path/to/ghz
```

The script builds the stand and LeetTest and runs every mode 5 times, which takes about 20
minutes. It prints this page's tables and writes them, with every run's raw output, to
`test/compare-ghz/out/`. Useful flags:

- `-runs N` sets the number of runs;
- `-modes B1` runs only the listed modes.

Numbers will differ on another machine. The pattern should not: in B1, ghz in synchronous mode
misses the freeze, while LeetTest and ghz with `--async` both show it.
