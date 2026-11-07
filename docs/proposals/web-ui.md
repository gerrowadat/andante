# Proposal: Users and a Web UI

Status: accepted, being built. The split into two services, users, and working out how to read a site
have shipped, and are in [`design/architecture.md`](../design/architecture.md) and
[`design/discovery.md`](../design/discovery.md). The rest of this is still to come.

This adds people and a website to `andante-server`. A user logs in, pastes a site's address or picks from what's already
indexed, chooses a pace, and gets a feed.

Nobody uses the API or has data to keep, so this assumes a clean start. There's no migration, and
`/api/v1` gets a new contract.

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

## Stack

Server-rendered Go: `net/http`, `html/template` and embedded static files, with [htmx](https://htmx.org)
vendored for the few places that need it, and a small classless stylesheet. There's no Node and no
build step, and every form works without JavaScript.

## Pages

| Page | What it does |
|---|---|
| Home | Your reads: position, next release, state |
| Browse | Every source: title, artwork, kind, entries, first and latest dates, a sample. Search, filter, and track one |
| Add | Paste an address, pick a candidate, check the sample, pick a pace. Goes to the existing source if there is one |
| Read | History, pause, resume, skip, jump, change pace, delete |
| Feed links | Copy a URL, add a person, revoke one |
| Feed page | `GET /f/{token}`, for anyone with the link and no login: the source, the pace, and how far along the read is |
| Source | One source's details, last error, entries, retry, and whether you're tracking it |
| Source edit | Title, kind, selectors, pause, delete. Admins only |
| Users | Invite, disable, reset, roles. Admins only |
| Account | Password, TOTP, linked logins, sessions, API tokens |

A new user starts on Browse, because the serial they want may already be there. Tracking a source
goes straight to the pace form, makes a read and shows the feed link. You can track the same source
twice, because reading a serial again is a new read. Browse doesn't show how many people follow a
source, or who.

The feed page is what a friend with a viewer link sees in a browser. Feeds today name the source and
link to it, but don't say how fast they're going, so the page spells that out in words, along with
the position, how much is left and the next release. It's on the public listener, it shows only what
the feed URL already grants, and it's rendered from the read. The feed's own description gets the
pace too.

The pace form has presets (one a day, weekdays, three a week, the original timing at 7x), and an
advanced box for an RRULE. The presets use the same shorthand as the CLI, so that code moves into a
shared package.

Any user can add a source. Sources are shared and cost publishers requests, so editing and deleting
are for admins.

## Login

Following the OWASP cheat sheets and NIST SP 800-63B. There's no public sign-up.

**Accounts.** The first admin comes from `andante-server user add --admin`, so there's never an
unclaimed setup page on the internet. After that, an admin sends a single-use invite link. There's
no email dependency, so password resets are an admin job or `andante-server user reset`.

**Passwords.** Argon2id, 64 MiB and 3 passes, with a limit on concurrent hashes so that login can't
be used to exhaust memory. 12 to 128 characters, no composition rules, and checked against an
embedded list of common passwords. Login failures all look the same, and take the same time whether
or not the user exists.

**Two-factor.** TOTP for password accounts, with single-use recovery codes stored hashed. Admins can
require it. Passkeys would be a good later addition.

**OIDC.** Authorization code flow with PKCE, `state` and `nonce`, using `coreos/go-oidc`. Any
compliant provider works. A user is matched on issuer and subject, never on email, because linking
on an email claim is how accounts get taken over. Someone new needs an invite, or must match
`--oidc-allowed-domain` or `--oidc-allowed-group`. Two-factor is the provider's job here.

**Sessions.** A random 256-bit ID in a `__Host-` cookie (`Secure`, `HttpOnly`, `SameSite=Lax`),
stored hashed so that a database leak can't be replayed. Rotated at login and on any change of
privilege. 7 days idle, 30 days absolute. Users can see and end their sessions.

**Requests.** `http.CrossOriginProtection` from the standard library, plus a per-session CSRF token
on forms. A strict Content-Security-Policy with no inline scripts, HSTS, and
`frame-ancestors 'none'`. `html/template` handles escaping.

**Abuse.** Rate limits by IP and by account, with backoff in place of a hard lockout, so that a
stranger can't lock someone out by guessing. `--trusted-proxies` says whose `X-Forwarded-For` to
believe. Login, invites, role changes and token actions go to an audit log.

## Configuration

`andante-server` gains these, with `ANDANTE_*` variables:

| Flag | Purpose |
|---|---|
| `--trusted-proxies` | CIDRs to trust for client addresses |
| `--password-login` | On by default |
| `--oidc-issuer`, `--oidc-client-id`, `--oidc-client-secret` | OIDC, off when unset |
| `--oidc-allowed-domain`, `--oidc-allowed-group` | Who can sign in without an invite |

## Delivery

Tests use `httptest`, and a small fake OIDC provider, so CI needs no real one. The tests that matter
most check that one user can't reach another's reads or tokens, on every route. 

## Phases

Each phase leaves a working system, and each is one PR.

| # | Delivers |
|---|---|
| 11 | Done: `andante-catalogue` with its database, API, revisions, `/changes` and demand. `andante-server` syncs and serves feeds from its copy |
| 12 | Done: users, personal API tokens, `user_id` on reads, the API on the main listener, encrypted feed tokens |
| 13 | Password login, sessions, invites, CSRF, headers, rate limits, audit log |
| 14 | Pages: Home, Browse, reads, feed links, sources, users, with per-user tests on every route |
| 15 | Done: discovery, canonical keys and close matches, through the API and CLI. The Add page moved to phase 14 |
| 16 | TOTP and OIDC |
| 17 | Compose, Nomad, docs, backups, and a security review before it faces the internet |

Nothing should go public before phase 16.

## What Changes in the Docs

- `docs/philosophy.md` says there are no accounts and no UI, that the admin API is the only way in,
  and that the public listener only serves feeds. All of that needs rewriting.
- `CLAUDE.md`'s layout, listeners and vocabulary change, and `docs/design/architecture.md` gets
  a new record.

## Questions Still Open

- [#31](https://github.com/gerrowadat/andante/issues/31): one binary for now, but keep asking whether
  the UI and API should split as this gets built.
