# Monitoring

Everything here is on `andante-server`'s ops listener and on `andante-catalogue`'s listener. Scrape
both.

## Health Endpoints

| Path | Answers | Use it for |
|---|---|---|
| `/healthz` | 200 while the process runs | Liveness |
| `/readyz` | 200 once the service's own database answers, within `--readiness-timeout` | Readiness and service checks |

## Metrics

Scrape `/metrics`. Source slugs are labels, which is fine because they're bounded by what
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
| `andante_feed_requests_total` | `format`, `role`, `status` | Feed requests |
| `andante_feed_render_duration_seconds` | | Time to render a feed |
| `andante_logins_total` | `result` | Login attempts: `ok`, `failed` or `throttled` |
| `andante_api_requests_total` | `route`, `status` | Admin API requests |

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

A `numeric` source's gaps are 404s, and those count as failures. If one has a lot of gaps,
raise the threshold for that source.
