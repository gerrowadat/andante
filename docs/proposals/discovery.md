# Proposal: Discovery

Status: proposed. Covers phases 5 and 9 of the [build plan](../plan/README.md).

Discovery builds and maintains the ordered index of a source's entries. It is
the only part of andante that talks to other people's websites.

## Strategies

```go
type Strategy interface {
    // Name is the value stored in sources.strategy.
    Name() string
    // Validate decodes and checks a source's config. Called on write.
    Validate(raw json.RawMessage) error
    // Next discovers entries after the given cursor, up to limit.
    // It returns the entries found, the cursor to resume from, and
    // whether the live end has been reached.
    Next(ctx context.Context, src Source, cur Cursor, limit int) (Batch, error)
}
```

A cursor is strategy-owned JSON, persisted in `sources.cursor` so a crawl
resumes after a restart.

Strategies are a compiled-in registry. Which one fits depends on the serial
more than its kind: most podcasts and many blogs keep their whole archive in
their feed (`feed`), most webcomics do not (`crawl` or `numeric`), and
WordPress sites of every kind can be walked with `feed-paged`. v1 ships four:

| Strategy | Use when | Config |
|---|---|---|
| `crawl` | Default. Walk pages by their "next" link. | `start_url`, optional extractor overrides |
| `numeric` | URLs carry an incrementing ID. | `url_template` (`{n}`), `start`, `max_consecutive_misses`, optional `format: html\|json` |
| `feed` | The feed holds the whole archive, or the site publishes RFC 5005 archived feeds (`rel="prev-archive"`). | `feed_url` |
| `feed-paged` | WordPress-style feeds that accept `?paged=N`. | `feed_url`, `max_pages` |

Later candidates (not v1): `webtoons` (episode numbers), `wayback` (rebuild
a feed's archive from Wayback Machine snapshots; see `../prior-art.md`), and a
backward crawl
via `rel="prev"` to extend an archive earlier than a nominated start page
(negative `seq`).

For `feed` and `feed-paged`, entries are ordered by published date and given
`seq` values after the full walk, oldest first.

## Entries from feeds

When entries come from a feed (`feed`, `feed-paged`), the feed item is the
primary record: title, link, guid, date, summary, full content
(`content:encoded`, Atom `content`), enclosures, and namespaced fields
(`itunes:duration`, `itunes:episode`, `itunes:season`, `itunes:image`,
`podcast:transcript`, `podcast:chapters`) kept in `extra` for pass-through.
An entry's identity is its guid where it has one, because podcast items often
share a link or have none; its `url` is the item's link, falling back to the
enclosure URL. An item with no guid, link or enclosure is skipped with a
warning. Show and site metadata (author, categories, `itunes:` channel fields)
is copied to `sources.extra`, and artwork to `sources.image_url`.
The entry's kind comes from the source's `default_kind`, overridden to
`episode` when an item has an audio or video enclosure. Page extraction runs
on the linked page only to fill gaps (a comic image a feed item lacks, the
full text of an article whose feed is summary-only — off by default for
articles, see below).

## Extraction

An `Extractor` pulls fields out of a fetched page. It is chosen by the entry's
kind: `page` (images first), `article` (main text first), `episode` (media
first). All of them prefer open metadata and fall back to heuristics, in this
order:

1. **schema.org JSON-LD** — `ComicStory`, `ComicIssue`, `Article`,
   `BlogPosting`, `NewsArticle`, `PodcastEpisode`, `CreativeWork`: `name`,
   `image`, `datePublished`, `associatedMedia`.
2. **Open Graph** — `og:title`, `og:image`.
3. **HTML link relations** — `<link rel="next">`, `<a rel="next">`, and their
   `prev` counterparts.
4. **Known platform patterns** — ComicControl (Hiveworks), the WordPress comic
   plugins (Webcomic, Toocheke, ComicPress/ComicEasel), common class names
   (`.comic-nav-next`, `a.next`, `#cc-comic`); for articles, WordPress and
   Ghost post navigation (`.nav-next a`, `rel="next"` in post footers); for
   episodes, `<audio>`/`<video>` sources.
5. **Readability** (articles only) — main-content extraction with an
   established Go port of Mozilla Readability, sanitised with an HTML
   sanitiser before storage.
6. **User overrides** — CSS selectors in the source config for any field. When
   set, these win.

Full-text extraction of articles is **off by default** and opt-in per source.
Where a publisher's feed gives only a summary, the default is to show the
summary and link to the original — the publisher chose that. Comic images and
podcast enclosures are different: they are what the publisher put in front of
every visitor, and andante links to them rather than copying them.

The extractor returns warnings along with fields ("no image found", "next link
points to the current page", "image is a site logo shared by 90% of pages").
Probes surface these warnings so a bad config is caught before saving.

Alt/title text matters for some comics (xkcd), so `alt_text` is a first-class
field: the `title` attribute of the comic image, or the JSON field in
`numeric` JSON mode.

## Politeness

- robots.txt is fetched per host, cached for 24 h and honoured (RFC 9309, via
  an established parser). A disallowed page puts the source in `error` with a
  clear message rather than being fetched.
- At most one request in flight per host, with at least `--crawl-interval`
  (default 5 s) between requests to the same host.
- Conditional requests: `If-None-Match` / `If-Modified-Since` from `crawl_log`.
- 429/503 honour `Retry-After`; other 5xx back off exponentially, capped at 6 h.
- `User-Agent` names andante, its version and the instance's base URL.
- Redirects are followed up to 5 hops and only over http(s). Response bodies
  are size-limited (default 5 MiB). Private and loopback addresses are refused
  unless explicitly allowed (SSRF guard: a source URL is user input).

## Lazy indexing

The crawl loop picks the source most in need of work:

- A source is **due** when the furthest read position on it is within
  `lookahead` (default 50) entries of the end of its index, or when it is not
  paused and has no reads yet (index the first `lookahead`).
- A source in **tail mode** is due every `tail_interval` (default 6 h, a flag).
  It re-fetches the last known page to look for a new next link, and if the
  source has a feed, checks that too.

The crawl scheduler records this in `sources.due_at`;
`POST /api/v1/sources/{id}/crawls` sets it to now.

A source enters tail mode when the strategy reports the live end: no next link,
a next link pointing back to the same page, or N consecutive misses
(`numeric`). It leaves tail mode when new entries appear.

## Loop and content-change protection

- A content hash per entry detects a "next" chain that cycles back to a page
  already indexed; the crawl stops and the source goes to `error`.
- If a known page's title or image changes on re-fetch, the entry is updated in
  place. `seq` never changes once assigned.
- Insertions into the middle of an archive (rare) are out of scope for v1.
  Detect them where cheap (a known page's next link now points to an unknown
  URL that is not at the end) and log a warning.

## Testing

`testdata/sites/<name>/` holds small fixture sites (5–20 pages each) that
mimic real platforms: ComicControl, WordPress + Webcomic, a numeric-ID site, a
JSON-API site in the style of xkcd, a site with only JSON-LD, a site with a
cycle, one with a missing next link mid-archive, a robots.txt disallow, one
returning 429 with `Retry-After`; and for other kinds, a podcast feed with
`itunes:`/`podcast:` tags and the full archive, a summary-only blog feed, a
WordPress blog crawlable by post navigation, and a paged news feed. Tests serve them with `httptest` and use a
fake clock for politeness timing. No test touches the real network.
