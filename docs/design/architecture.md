# Design: Architecture

How andante fits together: its components, data model, API and listeners. Built over
phases 3 to 9 of the [build plan](../plan/README.md). Why each part is the way it is
lives in the design record for that part.

## Components

```
              ┌──────────── admin listener (tailnet) ─────────────┐
 andante CLI ─┤  /api/v1/*   /healthz  /readyz  /metrics          │
 (pkg/client) └──────────────────────┬────────────────────────────┘
                                     │
                     ┌───────────────┼────────────────┐
                     ▼               ▼                ▼
                discovery        schedule           feed
            (crawler, strategies,  (pure math)   (render, decorators)
             extractors)              ▲                │
                     │                │                │
                     ▼                │                │
                 ┌──────────── Postgres ───────────────┘
                 │ sources, entries, reads, segments, tokens
                 └─────────────────────────────────────▲
                                                       │
              ┌──────────── public listener ───────────┴──────────┐
 feed readers ┤  GET /f/{token}.atom  .rss  .json                 │
              │  GET|POST /f/{token}/next   (owner tokens only)   │
              └───────────────────────────────────────────────────┘
```

- **andante-server** is one process: both listeners, plus the discovery loop in a
  background goroutine. One replica is the supported deployment; the crawl loop
  takes a Postgres advisory lock, so a second replica serves feeds but does not
  crawl.
- **andante** (CLI) is a separate binary that only talks HTTP to the admin API
  through the generated client in `pkg/client`.

## Configuration

Bootstrap only, through flags with environment variable counterparts (kong).
[`configuration.md`](../configuration.md) lists them. Every timing parameter is a flag.
On first start with no API tokens in the database and no `--admin-token`, the server
generates one, stores its hash and logs it once.

Migrations are embedded (goose + `embed.FS`) and run on start under a Postgres advisory
lock.

## Data model

```sql
sources (
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

crawl_log (
  id            bigint generated always as identity primary key,
  source_id     bigint not null references sources on delete cascade,
  url           text not null,
  status        integer,
  etag          text,
  last_modified text,
  fetched_at    timestamptz not null default now(),
  error         text
)
```

Feed tokens are high-entropy and looked up by hash, so the raw token exists
only in the URL the user holds. A token is shown once, at creation, and is
never stored or recoverable; that is why an OPML export issues new tokens.

## Admin API (v1)

OpenAPI 3.1 document at `api/openapi.yaml`, served at
`GET /api/v1/openapi.json` on the admin listener. Server interface and client are
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

## Public routes

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

Unknown or revoked tokens get 404 (not 401/403, to avoid confirming that a
token existed). Responses carry `ETag` and `Cache-Control: max-age=…` computed
from the next release time. Per-token rate limiting.

## Metrics (initial set)

`andante_crawl_requests_total{source,status}`, `andante_crawl_duration_seconds`,
`andante_entries_discovered_total{source}`, `andante_source_state{source,state}`,
`andante_feed_requests_total{format,role,status}`,
`andante_feed_render_duration_seconds`, `andante_probe_runs_total{result}`,
`andante_api_requests_total{route,status}`.

Source slugs are bounded by what the user creates, so they are acceptable as a
label. Tokens and read IDs are never labels.

## Where this diverged from the proposal

- **The flag table moved to `configuration.md`,** so that there is one list to keep
  current.
- **`reads.start_seq` came in a second migration.** The first segment's `from_seq` also
  counts the entries released at the start, so it could not say where a read began.
- **`sources.due_at` means "forced" when set,** and not simply the next crawl. See
  [`discovery.md`](discovery.md).
- **The admin listener defaults to loopback,** rather than assuming a tailnet. Where it
  is reachable from is a deployment choice. See [`deploying.md`](../deploying.md).
- **`GET /api/v1/version` and `andante_build_info` were added,** so that a client and an
  operator can both tell what is running.
- **`--test-clock-offset` was added** for the end-to-end suite, which runs the real
  binary and cannot wait a week for a replay. It moves reads and feeds. The crawl loop
  keeps the real clock, because the suite does not need it moved, and `discovered_at`
  comes from the database. The server logs a warning when it is set.
