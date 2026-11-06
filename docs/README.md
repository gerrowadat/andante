# andante documentation

Start at the [project README](../README.md) for what andante is.

## Understanding it

- [Design philosophy](philosophy.md) — the principles, and the open standards andante uses.
- [Prior art](prior-art.md) — existing archive-replay tools, and what andante does differently.
- [`proposals/`](proposals/) — designs not built yet:
  - [Architecture](proposals/architecture.md) — components, data model, API, listeners.
  - [Discovery](proposals/discovery.md) — the strategies and extractors still to build.
  - [Feed links](proposals/feed-links.md) — per-read link templates, such as for discussion.
- [`design/`](design/) — records for shipped features:
  - [Delivery](design/delivery.md) — CI, security scanning, releases, supply chain.
  - [API](design/api.md) — why the admin API's contract is the way it is.
  - [Discovery](design/discovery.md) — the crawl strategy, extraction, politeness, tail mode.
  - [Scheduling](design/scheduling.md) — pacing, segments and the arithmetic of a read.
  - [Feeds and sharing](design/feeds-and-sharing.md) — output formats, tokens, decorators.

## Building it

- [Build plan](plan/README.md) — phases, and a Claude Code prompt for each.
- [Development](development.md) — prerequisites, make targets, running CI locally.
- [Security](security.md) — what is scanned, triaging results, reporting a vulnerability.
- [Releasing](releasing.md) — cutting a release, what each job does, verifying an artifact.

## Running it

- [Configuration](configuration.md) — flags, environment variables, and what is not configured here.
- [API](api.md) — authentication, errors, pagination, versioning, generating a client.
- [Sources](sources.md) — adding a serial, what warnings mean, selectors, politeness.
- [Reads](reads.md) — the two pacing modes, adjustments, catch-up, downtime.
- [Feeds](feeds.md) — subscribing, sharing, revoking, the "next one now" link.

## Using it

Written as features land: `cli.md`, `deploying.md`, `monitoring.md`, `faq.md`.
