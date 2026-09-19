# What does commercial use look like?

[← Back to docs](README.md)

> This page describes an **intended** model. None of the paid part exists yet and there is no
> timeline. It is written down so that it does not come as a surprise later.

## What stays free

The core and the CLI are distributed under the Apache License 2.0 and will remain open. This is
a complete tool, not a demo: single-machine runs, any number of methods at any rate, CI
thresholds, reports. Commercial use is not restricted — a company can wire the CLI into its
pipelines without paying anyone or talking to anyone.

## What will be paid

A separate control plane, for teams whose needs outgrow one machine and one run:

- **distributed runs** — load from several machines, synchronized start, histograms merged
  correctly into one report;
- **run history** — stored results, release-over-release comparison, degradation tracked over
  time;
- **dashboard** — live view of a running test and analysis of finished ones;
- **integrations** — notifications, export to monitoring systems, role-based access.

The expected shape is a per-organization subscription with a free trial, priced against the
control plane rather than against requests or machines. A self-hosted option is under
consideration for closed environments.

## Why the line is drawn there

Everything that works from a single machine belongs to the open part. What is worth paying for is
what needs infrastructure: coordinating several generators, storing history, providing an
interface for a team.

Technically it is the same engine. The control plane is one more wrapper around the core,
alongside the CLI, and the metrics were designed from the start so that results from several
machines add up correctly.

## What it means for contributors

This is why a pull request is merged after the author signs the [CLA](../../CLA.md). It is a
license, not a transfer of ownership: you keep the copyright in your contribution while the
project gains the right to sublicense the code. Without it the closed part is legally impossible,
and it cannot be fixed after the fact — that would require the consent of everyone who ever
contributed.

If you use the open part, nothing changes for you: Apache 2.0 is granted irrevocably, and
released versions stay under it.
