# What is call chaining and why does it matter?

[← Back to docs](README.md)

Half the methods of a real service cannot be called with static data. To put load on money
transfers you need existing wallets: `Transfer` wants `from` and `to`, and those can only come
out of `CreateWallet` responses.

The usual workaround is to prepare test data up front and hard-code it into the config. That
holds until the first awkward question: a thousand transfers between the same two wallets is not
load on transfers, it is load on one database row and its lock. A realistic picture only appears
with a stream of distinct entities.

The other workaround is a script that creates a wallet and immediately transfers from it. Then
you are measuring "create plus transfer" as a sequence, and their contributions to latency can no
longer be separated.

We solve this with pools. One call is declared a producer: a field is taken from its responses
and accumulated in a named pool. Another call is a consumer: it fills its requests from that
pool. Each keeps its own RPS and stays a separate line in the report.

```yaml
calls:
  - method: wallet.v1.WalletService/CreateWallet
    rps: 5
    extract:
      wallets: wallet_id

  - method: wallet.v1.WalletService/Transfer
    rps: 50
    payload:
      from: ${pool.wallets}
      to: ${pool.wallets}
      amount: 100
```

Nobody's `.proto` has to be touched: the link is described on our side, while the existence of
the methods and fields is checked at startup against reflection data — a missing field is a clear
error before the run rather than garbage responses in the middle of it.

One question stays open until the implementation settles it: what to do when the consumer
outruns the producer and the pool drains — wait, reduce load, or fail. Silently reusing the last
value is the one answer that is certainly wrong.

Status: stage 1, once the engine works.
