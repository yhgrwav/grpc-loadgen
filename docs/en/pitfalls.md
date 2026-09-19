# Which load-testing problems does it fix?

[← Back to docs](README.md)

The ways load tools lie or get in the way, and where we stand on each.

The statuses are honest: `done` works today; `in progress` is being written now; `planned` means
the decision is fixed and the code is ahead of us.

| Problem | Status |
|---|---|
| Coordinated omission: an outage vanishes from the report | in progress |
| One method per run: a load mixture cannot be expressed | in progress |
| A closed model backs off exactly when the service degrades | in progress |
| The generator dies before the target, drowning in hung requests | planned |
| Percentiles get averaged and stop meaning anything | planned |
| Metrics recording slows down the generator itself | planned |
| `.proto` files and codegen required | planned |
| A typo in the config yields an empty green run | done |
| The target's rate limiting is counted as server errors | planned |
| A cold start skews the percentiles | in progress |
| Calls cannot be chained by data | stage 1 |
| No idea what is happening during a run | planned |

## Coordinated omission

The most expensive mistake in load testing. The target is 1000 RPS — one request every
millisecond. The service freezes for one second. A thousand requests were due during that
second.

Measured from the moment each request actually left, they all go out after the service recovers,
take 5ms each, and the report says `p99 = 5ms`. The whole outage is gone: the tool reported
everything was fine precisely when everything was not.

We stamp every request with its scheduled time at planning time and measure from there. A request
scheduled for `t=0` that left at `t=1000ms` and was answered at `t=1005ms` took `1005ms`. This
sits in the core from the first line — it cannot be bolted onto a finished engine.

## One method per run

Production is not a single endpoint. A service that separately handles 800 RPS of reads and
50 RPS of writes may fall over on their sum: a shared connection pool, locks, cache contention.
Separate runs cannot show it. We take a list of methods, each with its own RPS — one run, one
report.

## The closed model

"N virtual users, each waiting for a reply before the next request" feels natural and has a
built-in flaw: as the service slows down, the users wait longer and the offered load drops by
itself. The tool stops pushing exactly when pushing is the interesting part.

We use an open model: the target rate is a schedule, and requests go out on the clock whether or
not earlier ones came back.

## Killing the generator

The flip side of an open model: if the target stalls, hung requests pile up and the generator
dies before the service under test does. The cure is an explicit cap on concurrent requests.
Crucially, hitting that cap is a run error rather than a silent reduction of load — a "1000 RPS"
run quietly turned into "as much as we managed" is worse than a failed one.

## Percentiles and metrics

The average of two p99 values is not a p99 — they cannot be averaged. Only distributions can be
merged, so what crosses the boundary is a histogram and the percentiles are derived from it. The
same property is what makes multi-machine runs possible later.

Then there is the cost of recording. A mutex around a shared slice of latencies makes the
generator its own bottleneck at high rates: it ends up measuring lock contention rather than the
service. Hence HDR histograms and sharded counters.

## Preparation and typos

No `.proto` and no codegen: method definitions come from the service over reflection.

The config is read in strict mode. `rsp: 800` instead of `rps: 800` is an error that names the
spot, not a silent run at zero load with a green report. A zero `rps` is an error too: an empty
successful run is the worst kind of lie, because it looks like success.

## Errors and warm-up

A `RESOURCE_EXHAUSTED` reply from the target's rate limiter is not the same thing as a timeout or
a panic. Lumping them into "errors: 12%" throws away the meaning. The report keeps the categories
apart.

The first seconds of a run are always slow: empty caches, connections being established, JIT
warming up. They skew the percentiles of the whole run, so `warmup` keeps them out of the report.

## Visibility

A run must not be a ten-minute black box. While it works you can see the current rate, the number
of requests in flight, the error share and the current p99 — enough to abort a pointless run in
its second minute rather than its tenth.
