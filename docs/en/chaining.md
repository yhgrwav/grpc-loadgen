# What is call chaining and why does it matter?

[← Back to docs](README.md)

> This translation may lag behind the [Russian original](../ru/chaining.md).

> **Status: planned, not working yet.** The example below is a draft of the format, not a working
> config.

Half the methods of a real service cannot be called with static data. To load money transfers
you need existing wallets: `Transfer` needs `from` and `to`, and those only come from
`CreateWallet` replies.

The usual way out is to prepare test data in advance. Where data can be prepared, that is the
right path, and substitution from a file — a CSV or JSON dataset, one row per request — will
arrive before chaining. But not everything can be prepared: one-time tokens, sessions, load on the
creation itself. And a thousand transfers between the same two wallets are not load on transfers,
they are load on one database row and its lock.

The second way out is a script that creates a wallet and transfers from it right away. Then you
measure not the transfer but the sequence "create plus transfer", and the two can no longer be
told apart in the latency.

We solve it with pools. One call is declared a producer: a field is taken from its replies and
collected into a named pool. Another call is a consumer: it puts values from the pool into its
requests. Each runs at its own RPS and stays a separate line in the report.

```yaml
calls:
  - method: wallet.v1.WalletService/CreateWallet
    rps: 5
    extract:
      wallets: wallet_id

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    data:
      from: ${pool.wallets}
      to: ${pool.wallets}
      amount: 100
```

You don't touch someone else's `.proto`: the link is described on our side, and the methods and
fields are checked at start against reflection data — a missing field is a clear error before the
run, not garbage replies in the middle of the load.

An open question, settled with the implementation, is what to do when the consumer is faster than
the producer and the pool runs dry: wait, reduce load or fail. Silently reusing the last value is
the one answer known to be wrong.

Its place in the plan — after ramp-up and datasets, see the [strategy](../strategy.md) (Russian).
