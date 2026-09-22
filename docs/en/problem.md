# What problem does LeetTest solve?

[← Back to docs](README.md)

> This translation may lag behind the [Russian original](../ru/problem.md).

A load test exists to answer one question: **at what load does the service stop coping, and
where is the bottleneck.** That answer goes to the developers — to fix, to optimise, to decide
whether the release ships.

For the answer to be true, two things are needed.

**Load that looks like production.** Nobody loads a real service through a single endpoint. A
payment service at any moment handles hundreds of balance requests, dozens of transfers and a
handful of sign-ups — and it breaks on that mix, not on each method alone. A service that holds
800 RPS of reads and 50 RPS of writes separately can fall over on their sum — through a shared
connection pool, database locks, contention for the same cache. `ghz` takes one method per run:
three methods mean three runs and no answer to what happens when they run together. LeetTest
describes the whole load: a list of methods, each at its own RPS, one run, one report.

**Numbers you can trust.** A load tool produces a number that decisions are made on. A number
obtained the wrong way is more dangerous than no number: a missing number is visible, a wrong one
looks like knowledge. The costliest mistakes happen exactly during degradation — the service
slows down while the tool keeps showing pretty figures. The specific ways to lie are on
[a separate page](pitfalls.md).

What the tool does not do: it sees the service only from the outside — latency, throughput,
errors. It does not see the service's CPU or memory and does not guess them from its own data.
Those come from the service's monitoring and are lined up with the load over time.
