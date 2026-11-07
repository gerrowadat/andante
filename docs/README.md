# andante Documentation

Start at the [project README](../README.md) if you don't know what andante is yet.

## Using It

- [Running andante](running.md): start here. Docker Compose or Nomad, then add a serial and subscribe.
- [Sources](sources.md): choosing a strategy, adding a serial, what the warnings mean, selectors, politeness.
- [Reads](reads.md): the two pacing modes, adjustments, catch-up, downtime.
- [Feeds](feeds.md): subscribing, sharing, revoking, the "next one now" link.
- [CLI](cli.md): every `andante` command, with examples.
- [FAQ](faq.md): old dates, why a feed doesn't crawl, why a page was skipped.

## Operating It

- [Deploying](deploying.md): the listeners, Docker or a binary, exposing feeds, backups.
- [Monitoring](monitoring.md): metrics, alerts, health endpoints.
- [Configuration](configuration.md): flags, environment variables, and what isn't configured here.
- [API](api.md): authentication, errors, pagination, versioning, generating a client.

## Understanding It

- [Design philosophy](philosophy.md): the principles, and the open standards andante uses.
- [Prior art](prior-art.md): other archive-replay tools, and what andante does differently.
- [`design/`](design/): records of what shipped and why:
  - [Architecture](design/architecture.md): the two services, how they talk, data model, API, listeners.
  - [Delivery](design/delivery.md): CI, security scanning, releases, supply chain.
  - [API](design/api.md): why the API's contract is the way it is.
  - [Login](design/login.md): passwords, sessions, invites, throttling, the audit log.
  - [Discovery](design/discovery.md): strategies, extraction, politeness, tail mode.
  - [Scheduling](design/scheduling.md): pacing, segments and the arithmetic of a read.
  - [Feeds and sharing](design/feeds-and-sharing.md): output formats, tokens, decorators.
- [`proposals/`](proposals/): ideas that aren't built:
  - [Discovery](proposals/discovery.md): later strategies, and mid-archive insertions.
  - [Feed links](proposals/feed-links.md): per-read link templates, such as for discussion.
  - [Users and a web UI](proposals/web-ui.md): logins, per-user reads, and adding a site by its address.

## Building It

- [Build plan](plan/README.md): the phases, and the Claude Code prompt for each.
- [Development](development.md): prerequisites, make targets, CI.
- [Security](security.md): what's scanned, triaging results, reporting a vulnerability.
- [Releasing](releasing.md): cutting a release, what each job does, verifying an artifact.
