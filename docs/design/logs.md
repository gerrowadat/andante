# Design: Logs for Admins

Status: built for [#70](https://github.com/gerrowadat/andante/issues/70). There was no proposal.

Admins should be able to read the server's logs, and the logs for a feed or a crawl, from andante
itself. Before this the only way in was the process output, which on a Nomad or Compose deploy
means a shell on the host or a log pipeline that a small install may not have.

## What Shipped

- Each service wraps its `slog` handler in a ring buffer (`internal/logring`) that keeps the last
  `--log-history` records, 1000 by default. Records still go to standard error as before. The ring
  keeps what the handler would write at `--log-level`, and nothing below it.
- The catalogue serves its ring at `GET /v1/logs`, and its `crawl_log` table at
  `GET /v1/sources/{id}/crawl-log`. The server's API passes both through to admins at
  `GET /api/v1/logs?service=catalogue` and `GET /api/v1/sources/{id}/crawl-log`, and serves its own
  ring at `GET /api/v1/logs`. `GET /api/v1/sources/{id}/fetches` is the crawl log with only its
  requests, which is all it held when that endpoint shipped.
- The crawl log has events as well as requests: why a pass ran ("the furthest read is at 40 and it
  keeps 50 entries ahead"), what the strategy decided or found odd, what the pass indexed, and why
  a source stopped or is waiting on a host. Extraction warnings go there too.
- When the crawl gives up on finding a next page, the event keeps the body of the page it gave up
  on, at `GET /api/v1/sources/{id}/crawl-log/{entry}/page`, and `has_page=true` finds that event.
  A tail check that gets the whole page again (anything but a 304) and still finds no next link
  keeps the body it got.
- The website has a Logs page for admins, the crawl log on a source's page (with the kept page above
  it, behind a `<details>`, however old its event is), and a link from a read's page to its feed requests. The CLI has
  `andante logs`, `andante source crawl-log`, `andante source crawled-page` and
  `andante source fetches`.
- The public feed handler logs each request for a token that exists (`feed request`) and each use of
  the next-one-now control (`feed control`), with `read` and `token_id`. That's what the `read`
  filter matches.

## Decisions

**Memory, not a table.** Logs are for working out what just happened. A table would mean a database
write on every feed poll, plus pruning. Losing them on a restart is acceptable, because anyone who
needs them kept should be shipping standard error somewhere already. The crawl log was already a
table (it holds the validators for conditional requests), so it's read from there.

**Filtering by attribute.** Records carry `source` (a slug, which the crawl already logged) and
`read`. A filter is an exact match on those, which keeps the ring a dumb list and needs no index.

**The catalogue's logs go through the server.** The catalogue has never heard of a user, and isn't on
the public listener, so the server checks the admin and asks the catalogue, the same way it does for
probes.

**Events in the same table.** A crawl's events go in `crawl_log` beside its requests, with a `kind`
column, so that one ID order gives the whole story of a pass and one cursor pages through it. A
separate table would mean merging two lists by time to show them together.

**Notes go out as they happen.** A strategy says what it decided through its `PageFetcher`, the
same way it makes requests, so a note lands in the crawl log between the requests either side of
it, and one made before a strategy fails is still kept. A probe's fetcher turns the warnings into
the probe's own.

**One kept page per source.** A page body can be up to `--crawl-max-body` (5 MB by default), so
keeping every one would grow the table fast. The page that matters is the one the crawl is stuck
on, so keeping a new one drops the old. The website shows the first 256 KB of it, as text. It's
someone else's HTML, so it's never rendered.

**`/fetches` stays.** It's in `/api/v1`, so it can't change shape, and its `url` is required, which
an event may not have. It filters the crawl log to requests.

**What stays out.** Feed requests are logged with the token's ID, never the token. The bootstrap
service logs its token while there's no admin, so it gets the logger without the ring, and the token
reaches standard error only.

## Not Built

- Following a log live. Reload the page.
