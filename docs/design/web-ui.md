# Design: Users and a Web UI

This added people and a website to `andante-server`. Someone logs in, pastes a site's address or
picks from what's already indexed, chooses a pace, and gets a feed. It shipped over phases 11 to 19
of the [build plan](../plan/README.md), and the details are in their own records:

- [`architecture.md`](architecture.md): the split into a catalogue and a server.
- [`login.md`](login.md): passwords, sessions, two-factor, OIDC, throttling and the audit log.
- [`bootstrap.md`](bootstrap.md) and [`invites.md`](invites.md): how people get accounts.
- [`discovery.md`](discovery.md): working out how to read a site from its address.
- [`pages.md`](pages.md): the pages, and who can reach what.

Nobody used the API or had data to keep, so it assumed a clean start, with no migration. `/api/v1`
only grew, so it kept its contract.

## What Was Decided

| Question | Decision |
|---|---|
| Who uses it | Several users. A serial is crawled once, and each user has their own reads |
| Login | Local username and password, and generic OpenID Connect (OIDC). Either can be turned off |
| Exposure | Safe on the public internet behind a TLS proxy |
| Feed URLs | Can always be shown again |
| Structure | A catalogue service for feed-level data, and a separate user-facing service with its own model |
| Reads | Private to their user. You share one by handing out a viewer link |
| Editing sources | Admins only, for now |
| Friends | A viewer link needs no account |
| Binaries | `andante-server` stays one binary for the UI, API and feeds |

## Where It Diverged

The proposal had Compose, Nomad, backups and the security review in one phase. Bootstrapping and
invites from users came up while building it and got phases of their own (17 and 18), so the rest
moved to phase 19.

Users can invite people when an admin allows it, which the proposal didn't have. See
[`invites.md`](invites.md).

## Going Public

Phase 19 went through every route before calling this safe to put on the internet. The review is
in that phase's PR, and what it found was fixed there or filed as an issue.

Two alerts came with it, in [`deploy/prometheus/alerts.yml`](../../deploy/prometheus/alerts.yml):
lots of failed logins, and an OpenID provider that has stopped working. The throttle already slows
password guessing, so the first one is a warning, for knowing about it. `andante_sessions` counts
live sessions at scrape time, because sessions mostly end by expiring, and nothing happens then for
a counter to count.

## Still Open

- [#31](https://github.com/gerrowadat/andante/issues/31): whether the UI and API should be separate
  binaries. One for now.
- Passkeys would be a good later addition to login.
