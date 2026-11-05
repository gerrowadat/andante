# Phase 9 — Feeds as sources: podcasts, blogs, news, and more strategies

Read first: `CLAUDE.md`, `docs/philosophy.md` ("Any serial, not just comics"),
`docs/design/discovery.md`, and what remains of `docs/proposals/discovery.md`
("Entries from feeds", extraction for articles and episodes, strategies).
Before designing the article strategies, read how FeedRewind
(github.com/ilidemi/feedrewind, listed in `docs/prior-art.md`) discovers a
blog's full post list, and note in the PR which of its approaches you used or
rejected.

## Goal

Podcasts, blogs and news sites can be added from their feeds; comics with
numeric URLs or WordPress paged feeds can be added without crawling page by
page; articles can be crawled by post navigation.

## Do

- `feed`: parse with gofeed; keep the item as the primary record (title, link,
  guid, date, summary, `content:encoded`/Atom content, enclosures, and
  `itunes:`/`podcast:` fields into `extra`); follow RFC 5005 `prev-archive`
  links when present; order by date; assign `seq` oldest first. Identity is the
  guid where there is one; `url` is the item's link, falling back to the
  enclosure URL (`entries.url` is nullable, with partial unique indexes on
  `(source_id, guid)` and `(source_id, url)`). Kind from the
  source's `default_kind`, `episode` when an item has an audio/video
  enclosure. Copy artwork to `sources.image_url`, and author, categories and
  `itunes:` channel fields to `sources.extra`. Tail mode re-reads the live feed.
- `feed-paged`: walk `?paged=N` (configurable parameter name) until 404, an
  empty page or `max_pages`; then as `feed`.
- `numeric`: `url_template` with `{n}`, `start`, `max_consecutive_misses`
  (default 5); `format: html` (extract as usual) or `format: json` (field
  mapping for title, image, alt, date — e.g. xkcd's `info.0.json`). Gaps
  (404s within the archive) are skipped and do not consume a `seq`.
- Extractors: `article` (JSON-LD `Article`/`BlogPosting`/`NewsArticle`, Open
  Graph, post navigation patterns for `crawl`, and Readability-based full text
  using an established Go port — **opt-in per source, off by default** — with
  output sanitised by an established HTML sanitiser) and `episode` (JSON-LD
  `PodcastEpisode`, `<audio>`/`<video>` sources, enclosure type and length via
  a `HEAD` request only when the page does not give them).
- Feed-sourced entries run page extraction only to fill gaps (e.g. a comic
  image the feed item lacks), configurable per source.
- `POST /api/v1/imports/opml`: create `feed`-strategy sources from an OPML
  file in `paused` state, so nothing crawls until the user probes and enables
  each one. Report per-outline results. A Miniflux OPML export makes a good
  realistic test input.
- Probe support for all strategies and kinds; `source probe` in the CLI gains
  `--strategy` and `--kind`.
- Fixtures: a podcast feed with the full archive and `itunes:`/`podcast:`
  tags, an RFC 5005 archived feed chain, a summary-only blog feed, a WordPress
  blog crawlable by post navigation, a paged news feed, a numeric HTML comic,
  a JSON API comic.
- Update `docs/sources.md`: choosing a strategy by what the serial's feed holds;
  the full-text opt-in and why it is off by default.

## Out of scope

`webtoons` and backward crawling — leave them in the proposal. Downloading or
proxying enclosures (never).

## Acceptance criteria

- [ ] Each strategy indexes its fixture correctly, reaches the live end, and picks up a new entry in tail mode.
- [ ] A podcast fixture, end to end through phase 7's RSS output, yields a feed with correct enclosures, durations, artwork and andante-owned guids.
- [ ] A read with replay pacing at speed 7 over the podcast fixture releases on the expected days (uses phase 6's arithmetic with real dated entries).
- [ ] Full-text extraction is off unless enabled; sanitiser strips scripts, event handlers and iframes (tested).
- [ ] OPML import creates paused sources and reports duplicates without failing the whole import.

## Finish

Branch `phase-9-feeds-as-sources`. PR. Stop. Move the shipped strategies and
extractors into `docs/design/discovery.md`.
