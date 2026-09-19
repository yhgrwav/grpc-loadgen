# What problem does grpc-loadgen solve?

[← Back to docs](README.md)

Nobody ever loads a real service through a single endpoint. In a payments service there are, at
any moment, hundreds of balance reads, dozens of transfers and a handful of sign-ups — and it
breaks on that mixture, not on any one method in isolation.

The tools normally used for this work differently. `ghz` takes one method per run: three methods
mean three runs, three reports, and no answer at all to what happens when they run together. A
service that handles 800 RPS of reads and 50 RPS of writes separately may fall over on their sum
— a shared connection pool, database locks, contention over the same cache. Separate runs will
never show that.

grpc-loadgen describes the load as a whole: a list of methods, each with its own RPS and
duration, in one run and one report. That is the core difference — not "faster" or "nicer", but
the ability to ask a question the other tools cannot express.

The other half of the problem is trusting the numbers. A load tool produces a figure that
decides whether something ships. A figure obtained the wrong way is more dangerous than no
figure at all: a missing number is visible, a wrong one looks like knowledge. The specific ways
these tools lie have [their own page](pitfalls.md).
