# Why this tool?

[← Back to docs](README.md)

grpc-loadgen rests on a single principle: a load test is worth something only when its result can
be trusted without qualification. Everything else in the tool follows from that.

The priorities are ordered: **measurement correctness**, then **usability**, then **speed**. Where
they conflict the first one wins — an optimization that degrades accuracy is rejected.

## Load that mirrors production

The tool describes load the way it actually occurs: several methods at once, each at its own rate
and for its own duration, in one run and one report. It is the mixture of calls that surfaces
connection-pool contention, database locks and cache competition — effects that stay invisible
while methods are measured one at a time.

## Measurement that resists distortion

Latency is counted from the moment a request was scheduled, not from the moment it managed to
leave. This captures the delay in full, including time spent queued on the generator's side. When
the target freezes for one second, the tool reports `1005ms` — the delay a user would have
experienced — instead of the `5ms` that merely describe how fast the service answered once it
recovered.

Load is delivered on a schedule: the configured rate is maintained regardless of whether the
service keeps up. A run at 1000 RPS remains a run at 1000 RPS from beginning to end.

## No preparation required

An address and a method name are enough to start. The method definition comes from the service
itself over gRPC server reflection: no `.proto` files, no code generation, no build steps.
Interaction happens at the protobuf level, so the language and platform of the service under test
are irrelevant.

## Declarative description

Load is defined by a configuration file rather than a program. The file lives alongside the
service's code, goes through review, and still reads clearly six months later. It needs no
debugging and cannot itself become a source of distortion in the measurement.

## Ready for continuous integration

Threshold conditions are declared in the same file: exceeding a stated value terminates the
process with exit code `1` and stops the pipeline. The report goes to stdout and diagnostics to
stderr, so the outcome is machine-readable without parsing text with regular expressions.

## Regard for the person using it

Error messages name the file, the line and the cause. An unknown key in the configuration and a
rate of zero are treated as errors rather than silently accepted: a run that sent no requests must
not look like a success. The state of a run is visible while it happens — current rate, requests
in flight, error share, current p99.

The quality of the interface is held to the same standard as the correctness of the measurements:
an unclear error message counts as a defect.

## How this compares with existing tools

| Capability | ghz | k6 | JMeter | grpc-loadgen |
|---|---|---|---|---|
| Several methods at different rates in one run | one method per run | via script | via several thread groups | **in configuration** |
| Latency measured from scheduled time | partly | closed model by default | no | **yes** |
| Runs without `.proto` or code generation | yes | `.proto` required | `.proto` required | **yes** |
| Calls chained through response data | no | by hand in script | by hand | **stage 1** |
| Threshold conditions for CI | partly | yes | via plugins | **yes** |
| Load described without programming | yes | JavaScript script | XML through a GUI | **yes** |
