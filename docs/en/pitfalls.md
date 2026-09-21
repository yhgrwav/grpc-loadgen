# Which load-testing problems does it fix?

[← Back to docs](README.md)

> This translation may lag behind the [Russian original](../ru/pitfalls.md).

A list of places where load tools lie or get in the way, and where we stand on each.

Statuses are honest: `done` — works now; `planned` — the decision is made, the code is ahead.

| Problem | Status |
|---|---|
| Coordinated omission: service stalls vanish from the report | done |
| One method per run: a mixed load can't be expressed | done |
| Closed model lowers the load exactly when the service degrades | done |
| A timeout is recorded as a number and hides the tail | done |
| A sub-millisecond connection refusal "improves" latency | done |
| Percentiles get averaged and lose meaning | done |
| Metrics slow down the generator itself | done |
| `.proto` files and codegen required | done |
| A config typo gives an empty green run | done |
| Cold start skews percentiles | done |
| The generator dies before the target on hanging requests | done |
| The generator hits its own limit and the report blames the service | planned |
| Target rate limits counted as service errors | planned |
| Calls can't be chained on data | planned |
| No idea what is happening during the run | done |

## Coordinated omission

The costliest mistake in load testing. The target is 1000 RPS, a request every millisecond. The
service hangs for a second. During that second 1000 requests should have gone out.

If latency is counted from the moment of actual sending, they all go out after the service
recovers, take 5 ms each, and the report shows `p99 = 5ms`. The one-second stall has vanished —
the tool reported that all was well exactly when it wasn't.

We give each request its scheduled time when it is scheduled and count latency from it. A request
scheduled for `t=0` left at `t=1000ms`, the reply came at `t=1005ms` — latency `1005ms`. This is
built into the core from the first line: it cannot be bolted onto a finished engine.

## One method per run

Production isn't one method. A service that separately holds 800 RPS of reads and 50 RPS of
writes can fall over on their sum — shared connection pool, locks, cache contention. Separate runs
will never show it. We take a list of methods with their own RPS — one run, one report.

## Closed model

"N virtual users, each waits for a reply before the next request" feels natural but has a built-in
flaw: when the service slows down, users wait longer and the actual load drops by itself. The tool
stops pushing exactly when it gets interesting what happens under pressure.

We use an open model: the target RPS is a schedule, requests go out on the clock whether or not
the previous ones came back.

## Timeouts and connection failures

A request cut off by a two-second timeout told us one thing: it took **more** than two seconds.
Recording exactly two understates the tail; dropping it pretends it never happened. We don't
substitute a number: if cut-off requests could occupy a percentile's place, the report prints a
lower bound, `p99 >2.0s`. To see the tail, raise the timeout.

A refused connection comes back in a fraction of a millisecond. Recorded as a measurement, it
would drag latency down — the service is down while the report shows a speed-up. Such requests
count as failures but stay out of the percentiles.

## Percentiles and metrics

The average of two p99s is not a p99 — they must not be averaged. Only distributions can be added,
so the summary across methods is computed from merged histograms, not from ready-made
percentiles. The same will be needed when several machines apply the load.

Separately — the cost of recording metrics. A mutex around a shared latency slice makes the
generator its own bottleneck at high RPS. We record into HDR histograms: a write costs tens of
nanoseconds, a hundredth of a percent of the time at 5000 RPS — measured, not assumed.

## Preparation and typos

No `.proto` or codegen: method descriptions come from the service through reflection.

The config is read in strict mode. `rsp: 800` instead of `rps: 800` is an error that says where,
not a quiet run with zero load and a green report. A zero `rps` is also an error: an empty
successful run is the worst kind of lie, because it looks like success.

## Warm-up

The first seconds of a run are always slow: empty caches, connections being set up, JIT warming.
They skew the percentiles of the whole run, so `warmup` keeps them out of the percentiles.

## Generator death

The flip side of the open model: if the target stalls, hanging requests pile up and the generator
dies before the service under test. The cure is an explicit cap on concurrent requests. Silently
lowering the load at the cap is not allowed: a "1000 RPS" run that quietly became "whatever we
managed" is worse than a failed one. Today hitting the cap ends the run with an error. The plan is
to stop the run with a report and a verdict instead — "the service does not hold the requested
RPS" — which is the very answer the test was run for.

## The generator hits its own limit

If the machine can't keep up with the requested RPS, requests go out late, latency grows — and the
report blames the service when the generator is at fault. We already measure the generator's lag
separately from the service's response time. The plan is to turn it into a verdict: a run in which
the generator hit its limit is declared invalid, with what to change.

## Errors by category

A `RESOURCE_EXHAUSTED` from the target's rate limiter is not the same as a timeout or a service
crash. Lumping them into "errors: 12%" loses the meaning. The core tells the categories apart; the
report doesn't show the breakdown yet — today it has the total number of failures.

## Visibility

A run should not be a ten-minute black box. While it runs you see the current RPS, requests in
flight, the error share and the percentiles — enough to abort an obviously pointless run in the
second minute rather than the tenth.
