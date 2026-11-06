# Configuration

## How configuration works

`andante-server` takes flags, each with an `ANDANTE_*` environment variable
counterpart. A flag wins over its variable. These cover only what the process
needs to start. Sources, reads, schedules and tokens live in the database and
change through the API. There are no config files.

## Flags

| Flag | Env | Default | Purpose |
|---|---|---|---|
| `--database-url` | `ANDANTE_DATABASE_URL` | required | Postgres DSN |
| `--base-url` | `ANDANTE_BASE_URL` | required | Public URL used in feed links; absolute http(s) |
| `--listen` | `ANDANTE_LISTEN` | `:8080` | Public listener (feeds) |
| `--admin-listen` | `ANDANTE_ADMIN_LISTEN` | `127.0.0.1:8081` | Admin listener (API, health, metrics) |
| `--default-timezone` | `ANDANTE_DEFAULT_TIMEZONE` | `UTC` | IANA time zone for new reads |
| `--user-agent` | `ANDANTE_USER_AGENT` | `andante/<version> (+<base-url>)` | Crawler identity |
| `--crawl-interval` | `ANDANTE_CRAWL_INTERVAL` | `5s` | Minimum gap between requests to one host |
| `--admin-token` | `ANDANTE_ADMIN_TOKEN` | generated on first run | Bootstrap API token |
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

Durations use Go's format (`500ms`, `5s`, `2m`) and must be positive. How the crawl
settings fit together is in [sources](sources.md).

## The bootstrap API token

If `api_tokens` is empty when the server starts, it stores `--admin-token`, or
generates a token and logs it once at WARN. Save a generated token when it
appears: only its SHA-256 hash is stored, so it cannot be shown again. Once any
token exists, `--admin-token` is ignored.

## Startup

The server migrates the database before it starts listening. Migrations are
embedded in the binary and run under a Postgres advisory lock, so several servers
starting together apply them once. `/readyz` returns 200 once the database is
reachable with every migration applied.

The admin listener carries `/healthz`, `/readyz` and `/metrics`. Do not expose it
publicly. The public listener serves only feeds, so it can be.
