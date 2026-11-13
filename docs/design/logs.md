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
  `GET /v1/sources/{id}/fetches`. The server's API passes both through to admins at
  `GET /api/v1/logs?service=catalogue` and `GET /api/v1/sources/{id}/fetches`, and serves its own
  ring at `GET /api/v1/logs`.
- The website has a Logs page for admins, the crawl log on a source's page, and a link from a read's
  page to its feed requests. The CLI has `andante logs` and `andante source fetches`.
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

**What stays out.** Feed requests are logged with the token's ID, never the token. The bootstrap
service logs its token while there's no admin, so it gets the logger without the ring, and the token
reaches standard error only.

## Not Built

- Following a log live. Reload the page.
