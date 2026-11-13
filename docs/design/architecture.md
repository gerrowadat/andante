# Design: Architecture

How andante fits together: components, data model, API and listeners. Built over phases 3 to 9
of the [build plan](../plan/README.md). Why each part is the way it is lives in that part's
design record.

## Components

There are two services, and each has its own database, migrations, OpenAPI document and models,
so a cross-service join can't happen by accident.

```
              ┌──────────── public listener ────────────────────┐
 andante CLI ─┤  /api/v1/*   (needs a token)                    │
 (pkg/client) │                                                 │
 feed readers ┤  GET /f/{token}.atom  .rss  .json               │
              │  GET|POST /f/{token}/next   (owner tokens only) │
              └──────────────────────┬──────────────────────────┘
                                     │
                     ┌───────── andante-server ─────────┐
                     │ users, reads, segments, tokens   │
                     │ schedule (pure maths), feed      │
                     │ copy of sources and entries      │──▶ Postgres: andante
                     └───┬─────────────────────┬────────┘
                         │ catalogue API       │
                         ▼                     │
        ┌── andante-catalogue ──┐              │
        │ sources, entries      │              │
        │ crawling, discovery   │──▶ Postgres: catalogue
        └───────────────────────┘

 Prometheus ── ops listener: /healthz  /readyz  /metrics
```

- **andante-catalogue** is the infrastructure. It holds sources and entries, and does all the
  talking to other people's websites. It has never heard of a user or a read. Its API is private,
  with one service token, and only `andante-server` calls it. The crawl loop takes a Postgres
  advisory lock, so a second replica doesn't crawl.
- **andante-server** is everything a person touches: users, reads, tokens, feeds, and the API the
  CLI uses. It's one process with both listeners, plus the sync in a goroutine. One replica is the
  supported deployment.
- **andante** (CLI) is a separate binary that only talks HTTP to the API through the
  generated client in `pkg/client`.

## How the Services Talk

### Entries Flow Down

`andante-server` keeps its own copy of sources and entries, and only ever reads that copy. Feed
requests and the pacing arithmetic never call the catalogue. I looked at fetching live, but replay
needs every entry's dates on each request, and it would tie every feed to the catalogue being up.

Every source and entry gets a `revision` when it changes, from one sequence, in a trigger, so no
write can forget. The trigger takes an advisory lock until commit, which makes revisions commit in
order, so a reader that has seen a revision has seen everything below it. Every write that fires
the trigger takes the same lock first, so that two writers can't each hold what the other wants. A
single `UPDATE` would otherwise lock its row and then take the revision lock in the trigger, the
other way round to a crawl's transaction, and Postgres would kill one of them.

There's no log to prune. `GET /v1/changes?after=N&wait=25` reads the tables themselves, holds an
empty answer for up to `wait` seconds, and returns current rows in revision order, with tombstones
for deleted sources. The server stores the last revision it applied in the same transaction as the
changes, and a change at or below that is skipped, so two overlapping pulls do no harm.

A source's revision moves on every crawl and an entry's doesn't, so an entry can arrive before its
source. So nothing ties `entries.source_id` to `sources` in the copy.

### Release Times

An entry can't be released before it's been seen. In the copy that's `available_at`, set once, when
the entry first arrives. An entry can't count as released before the server could show it, however
far the sync falls behind, and release times stay stable. It also means the copy can't be rebuilt
from nothing without moving recent release times, so it gets backed up with everything else.

### Demand Flows Up

The server tells the catalogue how far readers have got with `PUT /v1/sources/{id}/demand`, and
`DELETE` when the last read goes. It does this when a read changes, and every `--demand-interval`,
because a read's position moves with the clock. The catalogue keeps `--crawl-lookahead` entries
ahead of that. If the server is down, the crawl stops growing and nothing else happens.

A serial is fetched once however many people read it, because the catalogue doesn't know how many
there are.

### What Happens When One Is Down

| Down | Still works | Stops |
|---|---|---|
| `andante-catalogue` | Feeds, releases until the copy runs out, reads, tokens, listing sources | Changing sources, new entries, probes. Those calls get a 503 |
| `andante-server` | Crawling up to the last demand | Feeds and the CLI |

The sync retries from 250ms, doubling up to 30s. A deleted source with a read stays in the copy.
The catalogue refuses to delete a source with demand, so that takes a race, and a read shouldn't pay
for it.

## Users

Logging in is in [`login.md`](login.md). Sources are shared and reads are private. A read has a `user_id` with a foreign key, and every store
method that reaches a read takes a `store.Actor`, so there's no way to fetch one without saying whose
it is. Someone else's is `ErrNotFound`. An admin's actor reaches anything. `store.Holder` is the same
thing for code that's already established the right, such as a feed request with a valid token, and
it's never for anything a user asked for by ID.

Feed tokens get their owner from their read, and API tokens have a `user_id`. Every route that takes
an ID is covered by a test that walks the OpenAPI document, so a new one fails until it's listed.

A feed token is looked up by its hash, and a copy is sealed with AES-256-GCM under `--secret` so that
its URL can be shown again. If someone gets the database and the secret, they get the feed URLs. A
feed URL only reads a feed and, for an owner token, releases the next entry, and I think that's a
fair trade for not making people subscribe again. A sealed check value in the database means the
server refuses to start with the wrong secret.

## Configuration

Bootstrap only, through flags with environment variable counterparts (kong).
[`configuration.md`](../configuration.md) lists them. Every timing parameter is a flag.
Users come from `andante-server user add`, which prints an API token.

Migrations are embedded (goose + `embed.FS`) and run on start under a Postgres advisory
lock.

## Data Model

```sql
sources (                                  -- in the catalogue; the server's copy has no cursor
  id            bigint generated always as identity primary key,
  slug          text unique not null,
  title         text not null,
  home_url      text not null,
  default_kind  text not null,            -- page | episode | article
  image_url     text,                     -- source artwork (podcast cover, site logo)
  extra         jsonb not null default '{}', -- copied show/site metadata (author, categories, itunes: channel fields)
  strategy      text not null,            -- crawl | numeric | feed | feed-paged
  config        jsonb not null,           -- typed per strategy, validated on write
  cursor        jsonb,                    -- strategy-owned resume state
  state         text not null,            -- indexing | tail | paused | error
  due_at        timestamptz,              -- next crawl; set by POST .../crawls and the crawl scheduler
  demand_seq    integer,                  -- catalogue only: how far readers have got
  revision      bigint not null default 0,  -- catalogue only
  last_crawled_at timestamptz,
  last_error    text,
  created_at    timestamptz not null default now()
)

entries (
  id            bigint generated always as identity primary key,
  source_id     bigint not null references sources on delete cascade,
  seq           integer not null,         -- reading order; 0 = crawl start, negative = earlier
  kind          text not null,            -- page | episode | article
  url           text,                     -- null only for a feed item with a guid but no link or enclosure
  guid          text,                     -- the source feed's own id, when it came from a feed
  title         text,
  summary       text,
  content_html  text,                     -- sanitised; full text for articles where available
  images        text[] not null default '{}',
  alt_text      text,
  enclosures    jsonb not null default '[]', -- [{url, type, length}]
  duration      interval,                 -- episodes
  extra         jsonb not null default '{}', -- pass-through namespaced fields (itunes:, podcast:)
  original_date timestamptz,
  content_hash  bytea,
  discovered_at timestamptz not null default now(),
  revision      bigint not null default 0,   -- catalogue; the server's copy has available_at instead
  unique (source_id, seq)
)
create unique index on entries (source_id, guid) where guid is not null;
create unique index on entries (source_id, url) where guid is null;

reads (
  id            bigint generated always as identity primary key,
  source_id     bigint not null references sources,
  name          text not null,
  start_seq     integer not null default 0, -- the first seq this read covers
  catch_up      text not null default 'passthrough', -- passthrough | buffer
  buffer        integer not null default 0,
  config        jsonb not null default '{}',  -- extension point (e.g. discussion links)
  created_at    timestamptz not null default now()
)

read_segments (                           -- append-only; see scheduling.md
  id            bigint generated always as identity primary key,
  read_id       bigint not null references reads on delete cascade,
  from_seq      integer not null,         -- last seq released when this segment starts
  starts_at     timestamptz not null,     -- what pacing counts from; can predate created_at
  pacing        text not null,            -- rrule | replay | paused
  rrule         text,                     -- pacing = rrule
  timezone      text not null,
  per_release   integer not null default 1, -- pacing = rrule
  speed         numeric,                  -- pacing = replay: 1 = original timing, 7 = weekly→daily
  min_gap       interval,                 -- pacing = replay: clamp each gap
  max_gap       interval,
  default_gap   interval,                 -- pacing = replay: used when an entry has no date
  reason        text not null,            -- created | pacing | pause | resume | skip | jump
  created_at    timestamptz not null      -- when it took effect; written from the caller's now, no default
)

feed_tokens (
  id            bigint generated always as identity primary key,
  read_id       bigint not null references reads on delete cascade,
  token_hash    bytea unique not null,    -- only the hash is stored
  role          text not null,            -- owner | viewer
  label         text not null,
  created_at    timestamptz not null default now(),
  revoked_at    timestamptz,
  last_seen_at  timestamptz
)

api_tokens (
  id            bigint generated always as identity primary key,
  token_hash    bytea unique not null,
  label         text not null,
  created_at    timestamptz not null default now(),
  revoked_at    timestamptz,
  last_used_at  timestamptz
)

crawl_log (                               -- catalogue only
  id            bigint generated always as identity primary key,
  source_id     bigint not null references sources on delete cascade,
  kind          text not null default 'fetch', -- fetch | event
  url           text,                     -- not null for a fetch
  status        integer,
  etag          text,
  last_modified text,
  fetched_at    timestamptz not null default now(),
  error         text,
  level         text,                     -- an event's: debug | info | warn | error
  message       text,                     -- an event's
  body          bytea                     -- the page an event gave up on; one per source
)
```

Feed tokens are high-entropy and looked up by hash. The server also keeps a copy
sealed with `--secret`, so that a feed URL can be shown again. An OPML export
still issues new tokens, because each person should get their own.

## API (v1)

OpenAPI 3.1 document at `api/openapi.yaml`, served at
`GET /api/v1/openapi.json`. Server interface and client are
generated with oapi-codegen (std-http server, strict mode). Errors are RFC 9457
problem details. Lists use opaque cursors (`?cursor=…&limit=…`) with a `next`
link in the body and a `Link` header. `PATCH` takes a JSON merge patch (RFC 7396).
A source's strategy is the `strategy` property of its `config`. See
[`api.md`](api.md).

| Method & path | Purpose |
|---|---|
| `GET/POST /api/v1/sources` | List / create sources |
| `GET/PATCH/DELETE /api/v1/sources/{id}` | Read / update / delete a source |
| `GET /api/v1/sources/{id}/entries` | Indexed entries, paginated |
| `POST /api/v1/sources/{id}/crawls` | Ask for a crawl now (returns 202) |
| `POST /api/v1/probes` | Dry-run discovery for a proposed source config; returns the first N entries and any extraction warnings; saves nothing |
| `GET/POST /api/v1/reads` | List / create reads |
| `GET/PATCH/DELETE /api/v1/reads/{id}` | Read (includes current position and next release time) / update / delete |
| `POST /api/v1/reads/{id}/adjustments` | `{"type": "skip" \| "pause" \| "resume" \| "jump" \| "pacing", ...}`; each appends a segment. A `jump` to a seq at or below the current position gets a 409 problem response |
| `GET /api/v1/reads/{id}/segments` | The read's history |
| `GET/POST /api/v1/reads/{id}/tokens` | List / issue feed tokens (raw token returned once) |
| `DELETE /api/v1/tokens/{id}` | Revoke a feed token |
| `POST /api/v1/exports/opml` | `{"issue": "owner" \| "viewer", "label": ...}`; issues a fresh token per read and returns OPML 2.0 of their feed URLs |
| `POST /api/v1/imports/opml` | Create `feed`-strategy sources from an OPML file |
| `GET/POST /api/v1/api-tokens`, `DELETE /api/v1/api-tokens/{id}` | Manage API tokens |
| `GET /api/v1/openapi.json` | This document (no auth) |
| `GET /api/v1/version` | The server's version (no auth) |

## Public Routes

| Route | Behaviour |
|---|---|
| `GET /f/{token}.atom` | Atom feed for the token's read |
| `GET /f/{token}.rss` | RSS 2.0 feed (what podcast apps expect) |
| `GET /f/{token}.json` | JSON Feed for the token's read |
| `GET /f/{token}/next` | Owner only: confirmation page for "release the next entry now" |
| `POST /f/{token}/next` | Owner only: appends a `skip` segment of 1, redirects to the original page |

ServeMux wildcards must be a whole path segment, so the three feed routes
share one pattern, `GET /f/{file}`, and the handler splits `{file}` into token
and format.

Unknown or revoked tokens get 404 (not 401 or 403, which would confirm that a
token existed). Responses carry `ETag` and `Cache-Control: max-age=…` computed
from the next release time. Per-token rate limiting.

## Metrics

`andante_crawl_requests_total{source,status}`, `andante_crawl_duration_seconds`,
`andante_entries_discovered_total{source}`, `andante_source_state{source,state}`,
`andante_feed_requests_total{format,role,status}`,
`andante_feed_render_duration_seconds`, `andante_probe_runs_total{result}`,
`andante_api_requests_total{route,status}`.

Source slugs are bounded by what the user creates, so they are acceptable as a
label. Tokens and read IDs are never labels.

## Where This Diverged From the Proposal

- **Feed tokens have no `user_id`.** The proposal gave them one, but they get it from their read, so
  the two can't disagree.
- **`/api/v1` didn't need a new contract.** The proposal expected a rewrite and a one-off exception to
  `spec-diff`, but users turned out to be additive: `GET /me`, `user_id` on reads, `?all`, `?q`, `?kind`
  and `urls` on feed tokens. What changed is who a call applies to, which a spec can't express.
- **The API moved to the public listener,** and the admin listener became ops, with only health and
  metrics. Every API route needs a token, so the listener that has none is the one kept private.
- **`--admin-token` went.** A first token in an env var is a secret that can't be rotated, and it only
  existed because there were no users.
- **The catalogue API is smaller than proposed.** The server reads sources from its copy, so
  there's no `GET /sources` or `GET /sources/{id}`. `wait` is in seconds, and clearing demand is a
  `DELETE`.
- **The copy's entries have no foreign key,** for the reason above. The proposal said revisions
  arrive in order, and they do, but a source is at its latest revision.
- **Held requests let go on shutdown.** The end-to-end suite found that a held `/changes` kept
  `SIGTERM` waiting for the whole shutdown timeout.

- **The flag table moved to `configuration.md`,** so that there is one list to keep
  current.
- **`reads.start_seq` came in a second migration.** The first segment's `from_seq` also
  counts the entries released at the start, so it could not say where a read began.
- **`sources.due_at` means "forced" when set,** and not simply the next crawl. See
  [`discovery.md`](discovery.md).
- **`GET /api/v1/version` and `andante_build_info` were added,** so that a client and an
  operator can both tell what is running.
- **`--test-clock-offset` was added** for the end-to-end suite, which runs the real
  binary and cannot wait a week for a replay. It moves reads and feeds. The crawl loop
  keeps the real clock, because the suite does not need it moved, and `discovered_at`
  comes from the database. The server logs a warning when it is set.
