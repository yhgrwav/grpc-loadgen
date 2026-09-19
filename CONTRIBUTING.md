# Contributing to grpc-loadgen

Thanks for your interest. The project is early work in progress — expect the
internals to move.

## Before you write code

**Open an issue first.** Describe the problem you hit or the change you have in
mind and wait for a reply. The architecture is still settling, and a pull
request that cuts across it may be rejected on grounds that have nothing to do
with its quality.

Bug reports and questions are welcome without any of the ceremony below.

## Contributor License Agreement

**Pull requests are merged only after the author has signed the
[CLA](CLA.md).**

The CLA is a license, not a transfer of ownership: you keep the copyright in
your contribution and may reuse it anywhere. It grants the project owner the
right to sublicense and relicense the project, so that a future license change
does not require tracking down every past contributor.

Signing takes one comment. When you open a pull request, the CLA assistant
replies with instructions; you sign by commenting:

```
I have read the CLA Document and I hereby sign the CLA
```

You sign once, and the signature covers your future contributions.

## Working on a change

Requirements: Go 1.24 or newer, and [golangci-lint](https://golangci-lint.run)
v2 for linting.

```console
$ go test ./...
$ golangci-lint run ./...
```

Both must pass before you open a pull request; CI runs them on Go 1.24 and on
the latest release.

Dependencies are restricted to permissive licenses (Apache-2.0, MIT, BSD, ISC).
CI fails on anything copyleft, so a change that pulls in a GPL-licensed module
cannot be merged.

## Conventions

- The library core under `pkg/` must not depend on the CLI, on the config file
  format, or on any particular output. Delivery mechanisms depend on the core,
  never the reverse.
- The core does not log. It returns errors and events; printing them is the
  CLI's job.
- Code is expected to read on its own. Doc comments on packages and exported
  identifiers are required; comments that narrate what the code does are not.
- Commit messages are written in English, in the imperative mood, explaining
  what changes and why.
