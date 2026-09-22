# Why this tool?

[← Back to docs](README.md)

> This translation may lag behind the [Russian original](../ru/why.md).

LeetTest is built on one principle: a load test result is worth something only when it can
be trusted without caveats. Everything else in the tool follows from that.

The priorities are ordered: **measurement correctness**, then **usability**, then **speed**. On
conflict the first wins — an optimisation that hurts accuracy is rejected.

## Load that mirrors production

The tool describes load the way it exists in reality: several methods at once, each with its own
rate and duration, all in one run and one report. It is on the mix of calls that contention for
the connection pool, database locks and cache fights show up — effects you never see while
methods are measured one at a time.

## Measurement that resists distortion

Latency is counted from the moment the request was scheduled for, not from the moment it could be
sent. That captures the whole delay, including time spent queued on the generator side. When the
target hangs for one second, the tool reports `1005ms` — the delay a real user would have seen —
instead of `5ms`, which only reflects how fast replies came after recovery.

Load is applied on a schedule: the requested RPS holds whether or not the service keeps up.

A request cut off by the timeout is not replaced with a number. All we know is that it took
longer than the timeout, and if such requests could occupy a percentile's place, the report
prints a lower bound instead of a number: `p99 >2.0s`.

## No preparation

A service address and a method name are enough to start. The tool gets the method description
from the service itself through gRPC server reflection: no `.proto` files, no codegen, no build
steps. The request body is written in the config as plain YAML and built from the service's schema
before the start.

## Declarative description

Load is defined by a config file, not a program. The file lives next to the service code, goes
through code review and stays readable six months later. It needs no debugging and cannot itself
become a source of measurement error.

## Care for the person using it

Error messages name the place and the reason. An unknown config key or a zero rate is an error,
not silently accepted: a run that sent no requests must not look successful. The run's state is
visible live — current rate, requests in flight, error share, percentiles.

Interface quality is treated on a par with measurement correctness: an unclear error message is a
defect.

## How it compares with existing tools

| Capability | ghz | k6 | JMeter | LeetTest |
|---|---|---|---|---|
| Several methods at different rates in one run | one method per run | via script | via several thread groups | **in the config** |
| Latency from the scheduled request time | partly | closed model by default | no | **yes** |
| No `.proto` or codegen needed | yes | `.proto` required | `.proto` required | **yes** |
| Load described without programming | yes | JavaScript script | XML through a GUI | **yes** |
| Ramp-up | yes | yes | yes | planned |
| Thresholds for CI | partly | yes | via plugins | planned |
| Chaining calls on response data | no | by hand in a script | by hand | planned |

