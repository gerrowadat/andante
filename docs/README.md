# andante documentation

Start at the [project README](../README.md) for what andante is.

## Understanding it

- [Design philosophy](philosophy.md) — the principles, and the open standards andante uses.
- [Prior art](prior-art.md) — existing archive-replay tools, and what andante does differently.
- [`proposals/`](proposals/) — designs not built yet:
  - [Architecture](proposals/architecture.md) — components, data model, API, listeners.
  - [Discovery](proposals/discovery.md) — strategies, extraction, crawl politeness, tail mode.
  - [Scheduling](proposals/scheduling.md) — RRULE schedules and the segment model.
  - [Feeds and sharing](proposals/feeds-and-sharing.md) — output formats, tokens, decorators.
  - [Delivery](proposals/delivery.md) — CI, security scanning, releases, supply chain.
- [`design/`](design/) — records for shipped features (empty until the first one lands).

## Building it

- [Build plan](plan/README.md) — phases, and a Claude Code prompt for each.

## Using it

Written as features land: `development.md`, `security.md`, `releasing.md`,
`configuration.md` (flags and env vars), `api.md`, `sources.md`, `reads.md`,
`feeds.md`, `cli.md`, `deploying.md`, `monitoring.md`, `faq.md`.
