# Proposal: Users and a Web UI

Status: accepted, being built. The split into two services, users, login with two-factor and OIDC,
working out how to read a site and the pages have shipped, and are in
[`design/architecture.md`](../design/architecture.md), [`design/login.md`](../design/login.md),
[`design/discovery.md`](../design/discovery.md) and [`design/pages.md`](../design/pages.md). What's
left is phase 17.

This adds people and a website to `andante-server`. A user logs in, pastes a site's address or picks from what's already
indexed, chooses a pace, and gets a feed.

Nobody uses the API or has data to keep, so this assumes a clean start, with no migration. `/api/v1`
only grew, so it kept its contract.

## What's Decided

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
| Binaries | `andante-server` stays one binary for the UI, API and feeds for now. This stays open as we build |

## Login

Shipped. Passkeys would be a good later addition.

## Delivery

Tests use `httptest`, and a small fake OIDC provider, so CI needs no real one.

## Phases

Each phase leaves a working system, and each is one PR.

| # | Delivers |
|---|---|
| 11 | Done: `andante-catalogue` with its database, API, revisions, `/changes` and demand. `andante-server` syncs and serves feeds from its copy |
| 12 | Done: users, personal API tokens, `user_id` on reads, the API on the main listener, encrypted feed tokens |
| 13 | Done: password login, sessions, invites, CSRF, headers, rate limits, audit log |
| 14 | Done: Home, Browse, reads, feed links, sources, users, the feed page, with per-user tests on every route |
| 15 | Done: discovery, canonical keys and close matches, through the API and CLI. The Add page moved to phase 14 |
| 16 | Done: TOTP with recovery codes, requiring it, and OIDC |
| 17 | Compose, Nomad, docs, backups, and a security review before it faces the internet |

Nothing should go public before the security review in phase 17.

## What Changes in the Docs

- This proposal moves to `docs/design/` when the last of it ships, probably as part of `login.md`.

## Questions Still Open

- [#31](https://github.com/gerrowadat/andante/issues/31): one binary for now, but keep asking whether
  the UI and API should split as this gets built.
