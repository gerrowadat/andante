# Configuration

## How Configuration Works

`andante-server` takes flags, each with an `ANDANTE_*` environment variable counterpart, and a
flag wins over its variable. These only cover what the process needs to start. Sources, reads,
schedules and tokens live in the database and change through the API. There are no config files.

andante is two services. These are the flags for `andante-server`. [`andante-catalogue`](#andante-catalogue)
has its own.

## Flags

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--database-url` | `ANDANTE_DATABASE_URL` | required | Postgres DSN |
| `--base-url` | `ANDANTE_BASE_URL` | required | Public URL used in feed links; absolute http(s) |
| `--listen` | `ANDANTE_LISTEN` | `:8080` | Public listener (feeds and the API) |
| `--ops-listen` | `ANDANTE_OPS_LISTEN` | `127.0.0.1:8081` | Ops listener (health, metrics) |
| `--trusted-proxies` | `ANDANTE_TRUSTED_PROXIES` | none | CIDRs of proxies whose `X-Forwarded-For` to believe. Set this behind a proxy, or every login looks like it comes from the proxy |
| `--password-login` | `ANDANTE_PASSWORD_LOGIN` | on | Allow logging in with a password. `--no-password-login` turns it off |
| `--catalogue-url` | `ANDANTE_CATALOGUE_URL` | required | Address of `andante-catalogue`, including `/v1` |
| `--catalogue-token` | `ANDANTE_CATALOGUE_TOKEN` | required | The catalogue's API token |
| `--demand-interval` | `ANDANTE_DEMAND_INTERVAL` | `30s` | How often to tell the catalogue how far readers have got |
| `--default-timezone` | `ANDANTE_DEFAULT_TIMEZONE` | `UTC` | IANA time zone for new reads |
| `--user-agent` | `ANDANTE_USER_AGENT` | `andante/<version> (+<base-url>)` | Crawler identity |
| `--crawl-interval` | `ANDANTE_CRAWL_INTERVAL` | `5s` | Minimum gap between requests to one host |
| `--secret` | `ANDANTE_SECRET` | required | Key for encrypted fields, at least 32 characters. See [the secret](#the-secret) |
| `--log-format` | `ANDANTE_LOG_FORMAT` | `json` | `json` or `text` |
| `--log-level` | `ANDANTE_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `--read-header-timeout` | `ANDANTE_READ_HEADER_TIMEOUT` | `5s` | Time allowed to read request headers |
| `--read-timeout` | `ANDANTE_READ_TIMEOUT` | `30s` | Time allowed to read a whole request |
| `--write-timeout` | `ANDANTE_WRITE_TIMEOUT` | `30s` | Time allowed to write a response |
| `--idle-timeout` | `ANDANTE_IDLE_TIMEOUT` | `2m` | How long an idle keep-alive connection stays open |
| `--shutdown-timeout` | `ANDANTE_SHUTDOWN_TIMEOUT` | `15s` | Time allowed for in-flight requests on shutdown |
| `--readiness-timeout` | `ANDANTE_READINESS_TIMEOUT` | `2s` | Time allowed for the `/readyz` check |
| `--crawl-lookahead` | `ANDANTE_CRAWL_LOOKAHEAD` | `50` | How far ahead of the furthest read to keep a source indexed |
| `--crawl-max-body` | `ANDANTE_CRAWL_MAX_BODY` | `5242880` | Largest response body to accept, in bytes |
| `--test-clock-offset` | `ANDANTE_TEST_CLOCK_OFFSET` | `0` | For the end-to-end suite only: runs reads and feeds this far ahead of the real clock. Never set it in production, because a release cannot be taken back |
| `--crawl-allow-private` | `ANDANTE_CRAWL_ALLOW_PRIVATE` | off | Allow crawling private and loopback addresses, for tests and trusted networks |
| `--tail-interval` | `ANDANTE_TAIL_INTERVAL` | `6h` | How often to check a source at its live end |
| `--crawl-poll-interval` | `ANDANTE_CRAWL_POLL_INTERVAL` | `30s` | How often the crawl loop looks for work when idle |
| `--crawl-request-timeout` | `ANDANTE_CRAWL_REQUEST_TIMEOUT` | `30s` | Time allowed for one request to a site |
| `--crawl-backoff-cap` | `ANDANTE_CRAWL_BACKOFF_CAP` | `6h` | Longest wait after a host fails or asks for one |
| `--robots-ttl` | `ANDANTE_ROBOTS_TTL` | `24h` | How long to cache a host's robots.txt |
| `--probe-timeout` | `ANDANTE_PROBE_TIMEOUT` | `5m` | Time allowed for a probe, which waits out `--crawl-interval` between pages |
| `--replay-min-dated-share` | `ANDANTE_REPLAY_MIN_DATED_SHARE` | `0.9` | Share of a source's entries that need dates before replay works without a `default_gap` |
| `--feed-rate-limit` | `ANDANTE_FEED_RATE_LIMIT` | `60` | Requests a feed token may make per hour |

Durations use Go's format (`500ms`, `5s`, `2m`) and must be positive.

## andante-catalogue

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--database-url` | `ANDANTE_CATALOGUE_DATABASE_URL` | required | Postgres DSN. A different database to the server's |
| `--api-token` | `ANDANTE_CATALOGUE_TOKEN` | required | The token `andante-server` must present |
| `--base-url` | `ANDANTE_BASE_URL` | required | This instance's public URL, named in the `User-Agent` |
| `--listen` | `ANDANTE_CATALOGUE_LISTEN` | `127.0.0.1:8082` | The API, `/healthz`, `/readyz` and `/metrics` |
| `--write-timeout` | `ANDANTE_CATALOGUE_WRITE_TIMEOUT` | `60s` | Must be over 30s, because a held `/changes` request can last that long |

| `--probe-timeout` | `ANDANTE_PROBE_TIMEOUT` | `5m` | Time allowed for a probe |

It also takes `--log-format`, `--log-level`, `--read-header-timeout`, `--read-timeout`,
`--idle-timeout`, `--shutdown-timeout` and `--readiness-timeout`, as above. How the crawl settings
fit together is in [sources](sources.md).

## Users

There are no users on a new database, and the server logs a warning until there's one. Add the first
from the command line, with the same `--database-url` or `ANDANTE_DATABASE_URL` as the server. It
prints that user's API token:

```sh
andante-server user add dave --admin
```

An admin can change sources and reach everyone's reads. Without `--admin`, a user only has their own
reads and tokens. A user starts with no password, so this prints a single-use link to set one. It lasts
a week:

```sh
andante-server user link dave --base-url https://feeds.example.com
```

After that, an admin can invite people with `andante invite add`. If an account is lost or compromised,
this revokes its API tokens, ends its sessions, clears its password and prints a new token:

```sh
andante-server user reset dave
```

## The Secret

`--secret` is the key for the encrypted copy of each feed token, which is what lets a URL be shown
again. Use `openssl rand -hex 32`. The server refuses to start with a different secret to the one
the database was set up with, so a typo can't quietly break anything.

To change it:

```sh
ANDANTE_NEW_SECRET=... andante-server rotate-secret
```

If it's lost, `--forget-urls` starts again with a new one. Feed tokens carry on working, because
they're looked up by hash, but their URLs can't be shown again and `token list` shows a dash.

## Startup

The server migrates the database before it starts listening. Migrations are embedded in the
binary and run under a Postgres advisory lock, so several servers starting together apply them
once. `/readyz` returns 200 once the database is reachable with every migration applied.

The ops listener carries `/healthz`, `/readyz` and `/metrics`, which have no auth, so don't expose it
publicly. The public listener has the feeds and the API. Don't expose the catalogue's listener either.

The server doesn't need the catalogue to start, and `/readyz` doesn't depend on it, because feeds
work without it.
