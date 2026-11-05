# Phase 5 — Discovery: the crawl strategy and page extraction

Read first: `CLAUDE.md`, `docs/philosophy.md` ("Be a good guest"),
`docs/proposals/discovery.md` (all of it), `docs/proposals/architecture.md`
(sources, entries, crawl_log).

## Goal

Given a start URL, andante can probe a comic (show the first pages it would
index, with warnings), and once the source is saved, index it lazily and
politely, then switch to tail mode at the live end.

## Do

- `internal/extract`: an `Extractor` interface chosen by entry kind, and the
  `page` extractor implementing the order from the discovery doc (JSON-LD →
  Open Graph → link relations → platform patterns → selector overrides), using
  goquery for HTML. Returns fields plus warnings. Use established packages for
  HTML parsing and JSON-LD rather than hand-rolled parsing. The `article` and
  `episode` extractors arrive in phase 9, but the interface, the entry model
  (`kind`, `enclosures`, `content_html`, `extra`) and the dispatch must already
  be kind-agnostic — nothing outside `internal/extract` may assume a comic.
- Probes report the share of entries with an original date (phase 6's replay
  pacing needs it).
- `internal/discovery`:
  - `Strategy` interface and a registry; `Validate` hooked into the API's
    source create/patch from phase 4. Cursors persist in `sources.cursor`.
  - A fetcher shared by all strategies: per-host politeness (one in flight,
    `--crawl-interval` spacing), robots.txt via an established RFC 9309
    parser with a 24 h cache, conditional requests from `crawl_log`,
    `Retry-After` and capped exponential backoff, redirect and body-size
    limits, honest User-Agent, SSRF guard (refuse private, loopback,
    link-local and metadata addresses after DNS resolution unless a flag
    allows them — the tests need that flag). Injected clock for tests.
  - The `crawl` strategy: follow next links from `start_url`; detect the live
    end and cycles; assign `seq` from 0.
  - The crawl loop: one goroutine, holding a Postgres advisory lock so only
    one replica crawls; sets `sources.due_at` per the "Lazy indexing"
    rules (lookahead default 50 — a flag) and picks sources that are due; tail
    mode with `--tail-interval`; updates `sources.state` and `last_error`.
- API: implement `POST /api/v1/probes` (runs the strategy for up to N pages,
  default 10, max 25, without saving; returns entries and warnings) and
  `POST /api/v1/sources/{id}/crawls` (sets `due_at` to now; 202).
- Metrics from the architecture doc's list for crawling and probes.
- Fixture sites in `testdata/sites/` as listed in the discovery doc's testing
  section, served by `httptest`. Each fixture is small, hand-written HTML that
  mimics its platform's real markup.
- `docs/sources.md`: adding a comic (probe first, then create), what the
  warnings mean, writing selector overrides, how politeness works and which
  flags tune it.

## Out of scope

`numeric`, `feed`, `feed-paged` (phase 9). Backward crawling. Reads and feeds.

## Acceptance criteria

- [ ] Probing each fixture site returns the expected entries; the bad ones return the expected warnings or errors.
- [ ] Politeness is tested with a fake clock: spacing, one-in-flight, Retry-After, backoff, robots disallow.
- [ ] SSRF guard tested, including a redirect to a private address.
- [ ] Lookahead and tail mode tested end to end against a fixture that grows a new page mid-test.
- [ ] No test touches the real network (CI has no special allowance; a test that tries fails).

## Finish

Branch `phase-5-discovery`. PR. Stop. Move the implemented parts of
`docs/proposals/discovery.md` to `docs/design/discovery.md`; leave the
phase 9 strategies behind in the proposal.
