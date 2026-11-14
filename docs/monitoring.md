# Monitoring

Everything here is on `andante-server`'s ops listener and on `andante-catalogue`'s listener. Scrape
both.

## Health Endpoints

| Path | Answers | Use it for |
|---|---|---|
| `/healthz` | 200 while the process runs | Liveness |
| `/readyz` | 200 once the service's own database answers, within `--readiness-timeout` | Readiness and service checks |

With a path in `--base-url`, such as `https://example.com/feeds`, the server's are under it:
`/feeds/healthz` and `/feeds/readyz`. The catalogue's stay where they are.

## Metrics

Scrape `/metrics`, or `/feeds/metrics` on the server with a path in `--base-url`. Source slugs are labels, which is fine because they're bounded by what
you create. Tokens and read IDs are never labels.

| Metric | Labels | Meaning |
|---|---|---|
| `andante_build_info` | `version`, `commit` | What's running |
| `andante_catalogue_requests_total` | `route`, `status` | Catalogue API requests |
| `andante_sync_changes_total` | | Catalogue changes applied to the server's copy |
| `andante_sync_failures_total` | | Failed attempts to sync |
| `andante_sync_last_success_timestamp_seconds` | | When the copy was last known to be current |
| `andante_crawl_requests_total` | `source`, `status` | Requests to sites. `status` is the HTTP status, or `backoff`, `disallowed` or `error` |
| `andante_crawl_duration_seconds` | | Time for a crawl pass |
| `andante_entries_discovered_total` | `source` | Entries indexed |
| `andante_source_state` | `source`, `state` | 1 for the state a source is in, 0 for the others |
| `andante_probe_runs_total` | `result` | Probes run |
| `andante_discoveries_total` | `result` | Attempts to work out how to read a site: `existing`, `found`, `none` or `failed` |
| `andante_discovery_stages_total` | `step` | Steps taken while working out how to read a site: `fetch`, `feed`, `feeds`, `paged` or `probe` |
| `andante_feed_requests_total` | `format`, `role`, `status` | Feed requests |
| `andante_feed_render_duration_seconds` | | Time to render a feed |
| `andante_logins_total` | `result` | Login attempts: `ok`, `failed` or `throttled` |
| `andante_two_factor_codes_total` | `result` | Two-factor codes: `ok`, `recovery`, `wrong` or `reused` |
| `andante_oidc_logins_total` | `result` | Logins with the OpenID provider: `ok`, `created`, `linked`, `refused` or `failed` |
| `andante_invite_uses_total` | `result` | Invites used: `ok`, or `refused` when the link was used up, withdrawn or out of date |
| `andante_sessions` | `method` | Live website sessions, by how they logged in: `password` or `oidc` |
| `andante_web_streams` | `stream` | Open event streams to the website's pages: `discovery` or `source` |
| `andante_web_stream_events_total` | `stream` | Events sent on those streams |
| `andante_web_streams_refused_total` | `stream` | Streams refused because someone had too many open |
| `andante_web_listen_failures_total` | | Times the server lost, or couldn't get, the database connection that hears about changed sources |
| `andante_api_requests_total` | `route`, `status` | API requests |
| `andante_log_records_total` | `level` | Records logged: `debug`, `info`, `warn` or `error` |

## Logs

Both services log to standard error, as JSON unless `--log-format` says `text`, and that's still
the place to ship them from. Each also keeps its last `--log-history` records (1000 by default) in
memory, so that an admin can read them without access to the host. A restart empties them.

On the website, admins get a **Logs** page. It shows either service, filtered by level, by a
source's slug, or (for the server) by a read's ID. A source's page shows an admin the catalogue's
crawl log for it: every request the crawl made, with the status or the error, and what the crawl
decided and why, such as why it ran, what it indexed, a page with no next link, or a host it's
waiting on. Warnings and errors are shaded. When the crawl gave up on finding a next page, that
event keeps the page as fetched, which you can expand to see what the crawl saw. Only the latest
such page per source is kept. The crawl log is in the catalogue's database, so it survives a
restart. A read's page links to the server's log of
requests for its feed.

The API has the same, for admins: `GET /api/v1/logs`, `GET /api/v1/sources/{id}/crawl-log` and
`GET /api/v1/sources/{id}/crawl-log/{entry}/page`.

A feed request is logged with the read's ID and the feed token's ID, never the token, since the
token is the credential. The bootstrap token is logged only to standard error, and never kept for
the page.

## Alerts

[`deploy/prometheus/alerts.yml`](../deploy/prometheus/alerts.yml) is a starting point, and
`make deploy-check` runs its unit tests.

| Alert | Fires when | What to do |
|---|---|---|
| `AndanteSourceInError` | A source has been in `error` for an hour | Read `last_error` on the source, fix the cause, and run `andante source edit <slug> --resume` |
| `AndanteCrawlFailures` | More than half of a source's requests have failed for 30 minutes | Check the site. andante backs off by itself, so this is about the site or your network |
| `AndanteFeedErrors` | More than 5% of feed requests have been a 5xx for 10 minutes | Check `/readyz` and the logs. It's usually the database |
| `AndanteSyncStale` | The server hasn't been in touch with the catalogue for an hour | Feeds still work, but nothing new is arriving. Check `andante-catalogue` |
| `AndanteNoSuccessfulCrawl` | Nothing has crawled successfully in 24 hours | Check the logs and the server's network. It also fires when every source is paused |
| `AndanteLoginFailures` | More than 50 failed logins every 15 minutes, for 15 minutes | Someone may be guessing passwords. The throttle slows them down. `andante audit` shows the addresses, and you can block them at the proxy |
| `AndanteOIDCFailures` | More than half of the logins with the OpenID provider have failed for 30 minutes | Check the provider, and the client ID and secret, in the server's logs |

A `numeric` source's gaps are 404s, and those count as failures. If one has a lot of gaps,
raise the threshold for that source.
