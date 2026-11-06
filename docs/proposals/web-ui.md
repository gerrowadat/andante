# Proposal: Two Services and a Web UI

Status: proposed, for review. Nothing here is built.

This splits andante in two and adds a website. One service knows about serials, and fetches and
stores them. The other knows about people: who they are, where they've got to, how fast they're
going, and their feeds. A user logs in, pastes a site's address or picks from what's already
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

## The Two Services

```
                     ┌──────────── andante-server ────────────┐
 browser ──┐         │ users, logins, reads, segments, tokens │
 CLI ──────┼─ HTTPS ▶│ web UI, /api/v1, /f/ feeds             │
 readers ──┘         │ copy of sources and entries            │
                     └───────┬─────────────────────┬──────────┘
                             │ catalogue API       │
                             ▼                     ▼
                     ┌── andante-catalogue ──┐   Postgres
                     │ sources, entries      │   database: andante
                     │ crawling, discovery   │
                     │ politeness, probes    │──▶ Postgres
                     └───────────────────────┘    database: catalogue
```

**`andante-catalogue`** is the infrastructure. It holds sources and entries, and does all the
talking to other people's websites. It has never heard of a user, a read or a schedule. Its API is
private, with one service token, and only `andante-server` calls it.

**`andante-server`** is everything a person touches. It owns users, reads, segments and tokens,
serves the UI and the feeds, and is the only API the CLI talks to. `internal/schedule` moves here
unchanged, because it's pure.

Each has its own database and Postgres role, so a cross-service join can't happen by accident. Each
has its own migrations, its own OpenAPI document and its own models.

| Today | Moves to |
|---|---|
| `internal/discovery`, `internal/extract` | `andante-catalogue` |
| `internal/schedule`, `internal/reads`, `internal/feed` | `andante-server` |
| `api/openapi.yaml` | `andante-server`, rewritten. `api/catalogue.yaml` is new |

## How Data Moves

### Entries Flow Down

`andante-server` keeps its own copy of sources and entries, and only ever reads that copy. Feed
requests, Browse and the pacing arithmetic never call the catalogue.

I looked at having feed requests fetch from the catalogue live. Replay needs every entry's dates on
each request, and it would tie every feed to the catalogue being up. The current design promises
that a broken crawl never breaks a feed, and a copy keeps that promise.

The catalogue gives every source and entry a `revision` when it changes, written under one lock so
that revisions commit in order. There's no separate log to prune:

```
GET /changes?after=18234&wait=30s
```

That long-polls and returns changed sources and entries in order, including tombstones for deleted
sources. The server stores the last revision it applied, in the same transaction as the changes.

### Release Times

Today an entry can't be released before its `discovered_at`. In the new model that comes from the
server's copy: `available_at` is set once, when an entry first arrives, and never changes. So an
entry can't count as released before the server could show it, however far the sync falls behind,
and release times stay stable.

This means the server's copy can't be rebuilt from nothing without moving recent release times. It
gets backed up with everything else.

### Demand Flows Up

Lazy indexing needs to know how far people have got. The server works out the furthest due position
for each source and tells the catalogue:

```
PUT /sources/{id}/demand
{"seq": 412}
```

The catalogue keeps `--crawl-lookahead` entries ahead of that. A source with no demand gets its
first `--crawl-lookahead` entries, as now. The server clears demand when the last read on a source
goes. If the server is down, the crawl just stops growing.

A serial is fetched once, whatever the number of readers, because the catalogue doesn't know how
many there are.

### What Happens When One Is Down

| Down | Still works | Stops |
|---|---|---|
| `andante-catalogue` | Login, feeds, releases until the copy runs out, Browse, reads | Adding sources, new entries, probes |
| `andante-server` | Crawling up to the last demand | Feeds, the UI, the CLI |

## The Catalogue API

Private. All of this is what the current API does for sources, plus changes, demand and discovery.

| Method and path | Purpose |
|---|---|
| `GET/POST /sources`, `GET/PATCH/DELETE /sources/{id}` | Sources |
| `GET /changes` | Changes after a revision |
| `PUT /sources/{id}/demand` | How far the index needs to go |
| `POST /sources/{id}/crawls` | Ask for a crawl |
| `POST /probes` | Try a config, saving nothing |
| `POST /discoveries` | Turn a site's address into candidate sources |

### Discovery

```json
{"url": "https://comic.example/"}
```

```json
{"candidates": [{
  "title": "Example Comic",
  "default_kind": "page",
  "config": {"strategy": "crawl", "start_url": "https://comic.example/comic/1"},
  "sample": [],
  "dated_share": 0.98,
  "warnings": [],
  "existing_source_id": null
}]}
```

It uses the same fetcher as probes, so robots.txt, spacing and the SSRF guard all apply. In order,
it:

1. Checks the address against each source's `home_url`, and stops there if one matches.
2. Follows `<link rel="alternate">` feeds, then tries common paths such as `/feed` and `/rss.xml`.
3. Checks each feed for RFC 5005 archives, `?paged=` support and a cut-off archive.
4. Looks for a first page to crawl from: `rel="first"`, or the platform patterns the extractors
   already know.

Each candidate then gets a normal probe. This is slow, because a request every 5 seconds is the
point, so the UI runs it in the background and shows progress. It won't find everything. If there's
no good candidate, the page asks for the first page's URL.

### Duplicates

Each source has a canonical key: its strategy plus its normalised feed URL, start URL or URL
template (lower-case host, no default port, no fragment, no trailing slash). The key has a unique
index, and `POST /sources` answers 409 with the existing source's ID.

This won't catch everything. The same comic added through its feed and by crawling gets two keys,
and a site that moves gets a new one. So candidates are also matched on title and home page, and
the UI asks before adding one that looks close.

`DELETE /sources/{id}` answers 409 while a source has demand, so a source with reads can't be
deleted.

## The User API

`andante-server` serves `/api/v1`, and the CLI uses it with a personal API token. Each token belongs
to a user, so `GET /reads` means your reads, and another user's read is a 404. Admins can see all of
them. Source calls are checked and passed on to the catalogue, and reads come from the local copy.

`GET /sources` gains `?q=` for search and `?kind=` for filtering.

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
| Source | One source's details, last error, entries, retry, and whether you're tracking it |
| Source edit | Title, kind, selectors, pause, delete. Admins only |
| Users | Invite, disable, reset, roles. Admins only |
| Account | Password, TOTP, linked logins, sessions, API tokens |

A new user starts on Browse, because the serial they want may already be there. Tracking a source
goes straight to the pace form, makes a read and shows the feed link. You can track the same source
twice, because reading a serial again is a new read. Browse doesn't show how many people follow a
source, or who.

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

## Authorisation

Every read and token row has a `user_id` with a foreign key, and every query goes through a store
that takes the user, so there's no way to fetch a read without saying whose it is. This is
application code, so a bug can still expose another user's reads, and the tests have to cover every
route.

## Feed Tokens

Tokens stay high-entropy and are looked up by hash. Each row also holds the token encrypted with
AES-256-GCM under `--secret`, so that the UI and CLI can always show a URL again, however it was
issued.

If someone gets the database and that key, they get every feed URL. A feed URL only lets you read a
feed and, for an owner token, release the next entry. I think that's a fair trade for not making
people re-subscribe. TOTP secrets use the same key, and rotating it needs a re-encryption command.

## Configuration

Flags with `ANDANTE_*` variables, and no config files.

**`andante-catalogue`** keeps today's crawl settings, and adds `--api-token` for its service token.

**`andante-server`** drops them, and has:

| Flag | Purpose |
|---|---|
| `--listen` | UI, API and feeds, default `:8080` |
| `--ops-listen` | `/healthz`, `/readyz` and `/metrics`, default `127.0.0.1:8081` |
| `--base-url` | Public address, for feed links, cookies and OIDC redirects |
| `--database-url` | Its own Postgres database |
| `--catalogue-url`, `--catalogue-token` | The catalogue |
| `--secret` | Key for encrypted fields |
| `--trusted-proxies` | CIDRs to trust for client addresses |
| `--password-login` | On by default |
| `--oidc-issuer`, `--oidc-client-id`, `--oidc-client-secret` | OIDC, off when unset |
| `--oidc-allowed-domain`, `--oidc-allowed-group` | Who can sign in without an invite |

The API moves onto the main listener, because users now hold their own tokens and it needs a login
like everything else. The ops listener keeps the things that have no auth.

## Delivery

Both binaries go in the image and release archives. The Compose stack gets a second service and a
second database, and the Nomad job gets a second task. Backups now mean two `pg_dump`s, and the
server's matters more, because the catalogue can be crawled again and people's reads can't.

`/readyz` on the server reports its database, and reports sync lag as a metric, not as a failure,
because feeds work without the catalogue. New metrics cover sync lag, logins and sessions.

Tests use `httptest`, and a small fake OIDC provider, so CI needs no real one. The tests that matter
most check that one user can't reach another's reads or tokens, on every route. A new one kills the
catalogue and checks that feeds still work.

`make spec-diff` fails on the rewrite, and `contracts` is a required check, so the PR that replaces
the contract needs a one-off exception in `scripts/spec-diff.sh`, removed straight after. The
additive-only rule applies from then on.

## Phases

Each phase leaves a working system, and each is one PR.

| # | Delivers |
|---|---|
| 11 | Split the repository: `andante-catalogue` with its database, API, revisions, `/changes` and demand. `andante-server` syncs and serves feeds from its copy |
| 12 | Users, personal API tokens and `user_id` everywhere. Rewritten `/api/v1`, CLI and end-to-end suite. Encrypted feed tokens |
| 13 | Password login, sessions, invites, CSRF, headers, rate limits, audit log |
| 14 | Pages: Home, Browse, reads, feed links, sources, users, with per-user tests on every route |
| 15 | Discovery, canonical keys and the add flow |
| 16 | TOTP and OIDC |
| 17 | Compose, Nomad, docs, backups, and a security review before it faces the internet |

Nothing should go public before phase 16.

## What Changes in the Docs

- `docs/philosophy.md` says there are no accounts and no UI, that the admin API is the only way in,
  and that the public listener only serves feeds. All of that needs rewriting.
- `CLAUDE.md`'s layout, listeners and vocabulary change, and `docs/design/architecture.md` gets
  a new record.

## Questions Still Open

- Should a user be able to see other users' reads of the same source, for a group read? This says
  no, and you share a read by handing out a viewer link.
- Should users edit a source they added? This says admins only.
- Should friends with a viewer link stay without an account? This assumes yes.
- Is one binary each right, or should `andante-server` split its UI from its API? I think one is
  enough, because they share a model and a login.
