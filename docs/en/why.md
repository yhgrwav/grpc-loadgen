# Why this tool?

[← Back to docs](README.md)

**One run instead of three.** Different RPS for different methods, at the same time. `ghz` takes
one method per run and simply cannot express a mixture. `k6` can, but through a JavaScript
program you have to write and maintain.

**Numbers you can trust.** Latency is measured from the moment a request was *scheduled*, not
from the moment it managed to leave. The difference is not cosmetic: when a service freezes for
a second, the naive measurement reports `p99 = 5ms` instead of an honest `1005ms`. Details in
[the list of problems](pitfalls.md).

**Zero preparation.** No `.proto`, no codegen, no scripting. An address and a method name are
enough — the tool asks the service itself for the method definition over gRPC server reflection.
The language the service is written in is irrelevant.

**A config, not a program.** Load is described declaratively, in one file that sits next to your
code and still reads six months later. In `k6` it is a script, and a script is code you debug,
review and eventually fix when it becomes the bottleneck itself.

**Made for CI.** Set `p99 < 200ms` and a breach fails the pipeline through the exit code. No
parsing output with regular expressions, no bash glue.

**Built for people.** A config error names the file, the line and what exactly is wrong. A typo
in a field name is an error, not a silent empty run with a green report. During a run you can see
what is happening instead of staring at nothing followed by a wall of numbers. We treat a poor
error message as a defect, not a detail.

| | ghz | k6 | JMeter | grpc-loadgen |
|---|---|---|---|---|
| Different RPS per method in one run | no | via script | awkward | **yes** |
| Latency free of coordinated omission | partly | closed model by default | no | **yes** |
| No `.proto`, no codegen | yes | no | no | **yes** |
| Data-driven call chaining | no | by hand in script | by hand | **stage 1** |
| CI thresholds | partly | yes | via plugins | **yes** |
| Config instead of code | yes | no | XML in a GUI | **yes** |
