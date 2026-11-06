# Proposal: Web UI

Status: proposed, for review. Nothing here is built.

A website for andante: log in, paste a site's address, pick a pace, get a feed. It's for people
who won't use the CLI, and for me when I'm not at a terminal.

## What's Decided

| Question | Decision |
|---|---|
| Who uses it | Several users. Sources are shared and crawled once, and each user has their own reads |
| Login | Local username and password, and generic OpenID Connect (OIDC). Either can be turned off |
| Exposure | Safe on the public internet behind a TLS proxy |
| Feed URLs | Can be shown again, for tokens issued through the UI |

## Shape of It

A new binary, `andante-web`, built on the same pattern as the CLI.

```
 browser ──HTTPS──▶ proxy ──▶ andante-web ──admin API──▶ andante-server ──▶ Postgres
                                  │                        (schema: public)
                                  └── sessions, users ───▶ Postgres (schema: web)
```

- **It's a client of the API.** It uses `pkg/client` with its own API token, and never reads
  andante's tables. "The API is the only way in" still holds.
- **It's separate from `andante-server`.** The server's public listener only serves feeds, and
  that stays true. Taking the web binary down, or never running it, changes nothing for feeds
  or the CLI.
- **The admin listener stays private.** Only `andante-web` needs to reach it.
- **Users live in `andante-web`.** It has its own migrations, in its own `web` schema. The server
  doesn't know what a user is.

The server's API token can do anything, so `andante-web` is what enforces who sees what. A bug
there can expose another user's reads. I'd accept that for a project this size, because the
alternative is accounts and permissions through the whole API.

## One Source, Many Reads

A serial is indexed once, however many people follow it. All that's specific to a user is their
read: where they are, how fast it's going, and their feed links. This is how andante already
works, because the crawl loop looks at the furthest read on a source, and adding a tenth reader
adds no requests to the publisher's site.

The UI has to keep it that way, so the same serial can't end up as two sources. See
[duplicates](#duplicates).

## Stack

Server-rendered Go: `net/http`, `html/template` and embedded static files, with [htmx](https://htmx.org)
vendored for the few places that need it, and a small classless stylesheet. There's no Node and
no build step, and no client state to keep in step with the server. Every form works without
JavaScript.

## API Additions

All additive, so they stay in `/api/v1`.

**Source listing.** `GET /sources` gains `?q=` for search and `?kind=` for filtering, so that Browse
doesn't have to page through everything.

**`owner` on reads.** An optional, opaque string on `POST /reads`, returned on reads, with a
`?owner=` filter on `GET /reads`. The server stores it and attaches no meaning to it.
`andante-web` sets it to the user's ID. Reads made with the CLI have no owner, and only admins
see those in the UI.

**`POST /discoveries`.** Takes a site's address and returns ranked candidate sources:

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
  "warnings": []
}]}
```

It uses the same fetcher as probes, so robots.txt, spacing and the SSRF guard all apply. In order,
it:

1. Follows `<link rel="alternate">` feeds, then tries common paths such as `/feed` and `/rss.xml`.
2. Checks each feed for RFC 5005 archives, `?paged=` support and a cut-off archive.
3. Looks for a first page to crawl from: `rel="first"`, or the platform patterns the extractors
   already know.

Each candidate then gets a normal probe. This will be slow, because a request every 5 seconds is
the point, so the UI runs it in the background and shows progress. It won't find everything. If
there's no good candidate, the page asks for the first page's URL and goes from there.

### Duplicates

Each source gets a canonical key: its strategy plus its normalised feed URL, start URL or URL
template (lower-case host, no default port, no fragment, no trailing slash). The key has a unique
index.

- `POST /discoveries` marks a candidate that matches a source with `existing_source_id`, and
  checks the address against each source's `home_url` before fetching anything. The UI then offers
  to track the existing source, and the site gets no requests.
- `POST /sources` answers 409 with the existing source's ID if the key is taken, so the CLI and
  OPML import are covered too.

This won't catch everything. The same comic added through its feed and by crawling gets two keys,
and a site that has moved gets a new one. A candidate is also matched on its title and home page,
and the UI asks before adding one that looks close. Admins can delete a duplicate that gets
through, as long as nobody has a read on it.

Existing databases might already hold duplicates, so the migration has to find those first.

## Pages

| Page | What it does |
|---|---|
| Home | Your reads: position, next release, state |
| Browse | Every source: title, artwork, kind, entries, first and latest dates, a sample. Filter and search, and track one |
| Add | Paste an address, pick a candidate, check the sample, pick a pace. Goes to the existing source if there is one |
| Read | History, pause, resume, skip, jump, change pace, delete |
| Feed links | Copy a URL, add a person, revoke one |
| Source | One source's details, last error, entries, retry, and whether you're tracking it |
| Source edit | Title, kind, selectors, pause, delete. Admins only |
| Users | Invite, disable, reset, roles. Admins only |
| Account | Password, TOTP, linked logins, sessions |

The pace form has presets (one a day, weekdays, three a week, the original timing at 7x), and an
advanced box for an RRULE. The presets use the same shorthand as the CLI, so that code moves out
of `cmd/andante` into a shared package.

A new user starts on Browse, because the serial they want may already be there. Tracking a source
goes straight to the pace form, makes a read and shows the feed link. The entries are already
indexed, so it works at once. You can track the same source twice, because reading a serial again
is a new read. Browse shows sources in `indexing`, `tail` or `error`. It doesn't show how many
people follow a source, or who.

Any user can add a source. Sources are shared and cost publishers requests, so editing and
deleting are for admins. Deleting a source with reads is already refused with a 409.

## Login

Following the OWASP cheat sheets and NIST SP 800-63B. There's no public sign-up.

**Accounts.** The first admin comes from `andante-web user add --admin`, so there's never an
unclaimed setup page on the internet. After that, an admin sends a single-use invite link. There's
no email dependency, so password resets are an admin job or `andante-web user reset`.

**Passwords.** Argon2id, 64 MiB and 3 passes, with a limit on concurrent hashes so that login
can't be used to exhaust memory. 12 to 128 characters, no composition rules, and checked against
an embedded list of common passwords. Login failures all look the same, and take the same time
whether or not the user exists.

**Two-factor.** TOTP for password accounts, with single-use recovery codes stored hashed. Admins
can require it. Passkeys would be a good later addition.

**OIDC.** Authorization code flow with PKCE, `state` and `nonce`, using `coreos/go-oidc`. Any
compliant provider works. A user is matched on issuer and subject, never on email, because
linking on an email claim is how accounts get taken over. Someone new needs an invite, or must
match `--oidc-allowed-domain` or `--oidc-allowed-group`. Two-factor is the provider's job here.

**Sessions.** A random 256-bit ID in a `__Host-` cookie (`Secure`, `HttpOnly`, `SameSite=Lax`),
stored hashed so that a database leak can't be replayed. Rotated at login and on any change of
privilege. 7 days idle, 30 days absolute. Users can see and end their sessions.

**Requests.** `http.CrossOriginProtection` from the standard library, plus a per-session CSRF
token on forms. A strict Content-Security-Policy with no inline scripts, HSTS, and
`frame-ancestors 'none'`. `html/template` handles escaping.

**Abuse.** Rate limits by IP and by account, with backoff in place of a hard lockout, so that a
stranger can't lock someone out by guessing. `--trusted-proxies` says whose `X-Forwarded-For` to
believe. Login, invites, role changes and token actions go to an audit log in the `web` schema.

## Feed URLs

The server keeps storing only hashes, and doesn't change. When `andante-web` issues a token, it
stores the URL in its own schema, encrypted with AES-256-GCM under `ANDANTE_WEB_SECRET`, so the
page can show it again.

If someone gets the database and that key, they get the feed URLs for tokens issued through the
UI. A feed URL only lets you read a feed and, for an owner token, release the next entry. I think
that's a fair trade for not making people re-subscribe. Tokens made with the CLI can't be shown,
and the page says so. TOTP secrets use the same key, and rotating it needs a re-encryption
command.

## Configuration

Flags with `ANDANTE_WEB_*` variables, and no config files.

| Flag | Purpose |
|---|---|
| `--listen` | Web listener, default `:8090` |
| `--base-url` | Public address, for cookies and OIDC redirects |
| `--api-url`, `--api-token` | The admin API |
| `--database-url` | Postgres |
| `--secret` | Key for encrypted fields |
| `--trusted-proxies` | CIDRs to trust for client addresses |
| `--password-login` | On by default |
| `--oidc-issuer`, `--oidc-client-id`, `--oidc-client-secret` | OIDC, off when unset |
| `--oidc-allowed-domain`, `--oidc-allowed-group` | Who can sign in without an invite |

## Delivery

`andante-web` joins the image and release archives. The Compose stack and Nomad job get it as a
second service. It has its own metrics under `andante_web_`, covering logins and sessions.

Tests use `httptest` and a small fake OIDC provider, so CI needs no real one. The tests that matter
most check that one user can't reach another's reads or feed links, on every route. The end-to-end
suite gets a scenario from login to a working feed.

## Phases

| # | Delivers |
|---|---|
| 11 | `owner` on reads, source search, canonical keys and duplicate refusal, `POST /discoveries`, pacing shorthand in a shared package |
| 12 | `andante-web`: schema, password login, sessions, invites, CSRF, headers, rate limits, audit log |
| 13 | Reads, Browse, the add flow and feed links, with per-user tests on every route |
| 14 | TOTP and OIDC |
| 15 | Packaging, Compose, Nomad, docs, end-to-end scenario, a security review before it faces the internet |

Phase 12 isn't safe to expose until phase 14 lands, so nothing should go public before then.

## What Changes in the Docs

`docs/philosophy.md` says v1 has no accounts and no UI. If this goes ahead, that needs rewriting,
because accounts now exist, in `andante-web` only. `docs/design/` gets a record when this ships.

## Questions Still Open

- Should a user be able to see other users' reads of the same source, for a group read? Today this
  says no, and you share a read by handing out a viewer link.
- Should users edit a source they added? This says admins only.
- `POST /sources` starts refusing duplicates, which changes behaviour without changing the schema.
  oasdiff won't flag it, but a client that added the same config twice would now get a 409. I think
  that's fine, because 409 is already a documented answer there.
- A feed reader's viewer tokens don't need a login. Should friends without an account stay that way?
  This assumes yes.
