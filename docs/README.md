# andante Documentation

Start at the [project README](../README.md) if you don't know what andante is yet.

## Using It

- [Installing](install.md): start here. Docker Compose, Nomad, plain Docker, a release archive or source.
- [Bootstrapping](bootstrap.md): the first admin, the CLI, invites, and what to do before going public.
- [Using andante](using.md): add a serial, make a read, subscribe, on the website or the CLI.
- [Sources](sources.md): choosing a strategy, adding a serial, what the warnings mean, selectors, politeness.
- [Reads](reads.md): the two pacing modes, adjustments, catch-up, downtime.
- [Feeds](feeds.md): subscribing, sharing, revoking, the "next one now" link.
- [CLI](cli.md): every `andante` command, with examples.
- [FAQ](faq.md): old dates, why a feed doesn't crawl, why a page was skipped.

## Operating It

- [Deploying](deploying.md): the listeners, exposing andante, backups.
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
  - [Users and a web UI](design/web-ui.md): the decisions behind accounts and the website, and where to find each part.
  - [Login](design/login.md): passwords, sessions, invites, throttling, the audit log.
  - [Pages](design/pages.md): the website, who can reach what, and the feed page.
  - [A better website](design/website.md): why htmx, Server-Sent Events and Pico CSS, and the themes.
  - [Bootstrapping](design/bootstrap.md): the token that makes the first admin without a shell.
  - [Invites](design/invites.md): users inviting people, links for more than one, who invited whom.
  - [Logs](design/logs.md): letting admins read the services' logs and the crawl log.
  - [Serving from a path](design/root-path.md): andante under `example.com/feeds`.
  - [Discovery](design/discovery.md): strategies, extraction, politeness, tail mode.
  - [Scheduling](design/scheduling.md): pacing, segments and the arithmetic of a read.
  - [Feeds and sharing](design/feeds-and-sharing.md): output formats, tokens, decorators.
- [`proposals/`](proposals/): ideas that aren't built:
  - [Discovery](proposals/discovery.md): later strategies, and mid-archive insertions.
  - [Feed links](proposals/feed-links.md): per-read link templates, such as for discussion.
  - [More for the website](proposals/website.md): a live Read page, and uploaded or per-user themes.

## Building It

- [Build plan](plan/README.md): the phases, and the Claude Code prompt for each.
- [Development](development.md): prerequisites, make targets, CI.
- [Security](security.md): what's scanned, triaging results, reporting a vulnerability.
- [Releasing](releasing.md): cutting a release, what each job does, verifying an artifact.
