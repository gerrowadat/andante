# Design: Discovery

Status: shipped in phase 5 of the [build plan](../plan/README.md): the `crawl`
strategy, the `page` extractor, the fetcher, probes and the crawl loop. The other
strategies and extractors are in the [proposal](../proposals/discovery.md). How to
add a serial is in [`sources.md`](../sources.md).

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
    Next(ctx context.Context, fetcher PageFetcher, src Source, cur Cursor, limit int) (Batch, error)
}
```

A cursor is strategy-owned JSON, persisted in `sources.cursor` so a crawl
resumes after a restart.

A cursor is strategy-owned JSON, persisted in `sources.cursor` so a crawl resumes
after a restart.

Strategies are a compiled-in registry. `crawl` walks pages by their "next" link.
Its config is `start_url`, plus optional selector overrides that are compiled when
the config is written, so a bad selector fails then and not at crawl time.

`numeric` fetches a URL template by number. A 404 is a gap, and a run of them is the
live end. The cursor keeps the next number after the last hit, so a tail check tries
the same IDs again.

`feed` reads a feed and follows `rel="prev-archive"` (RFC 5005), and `feed-paged`
walks `?paged=N` until a 404, an empty page, a repeat of the first page or
`max_pages`. Both must see the whole archive before they can order it, so a first
walk returns everything at once, oldest first, and only `fill_gaps` keeps to the
batch limit. Their cursor holds an 8-byte hash of each item handed over. A tail check
is a single conditional request.

### Entries from feeds

The feed item is the primary record. An entry's identity is its guid, because podcast
items often share a link or have none, and its `url` is the link, falling back to the
enclosure URL. An item with none of these is skipped with a warning. Namespaced fields
(`itunes:duration`, `itunes:episode`, `itunes:season`, `itunes:image`,
`podcast:transcript`, `podcast:chapters`) go to `extra`. Show metadata goes to
`sources.extra`, and artwork to `sources.image_url`. The kind is `default_kind`,
overridden to `episode` by an audio or video enclosure. Feed content goes through the
same sanitiser as page content, because a feed is no more trusted than a page.

### What came from FeedRewind

FeedRewind solves the same problem for blogs, so phase 9 studied it. andante took its
feed-first order and its warning for a feed cut off at a round number. It did not
take the heuristics that find archive pages with generated XPaths: `crawl` with
selectors covers those sites, with rules that a person can read.

## Extraction

An `Extractor` pulls fields out of a fetched page. It is chosen by the entry's
kind, and only `page` (images first) exists so far. It prefers open metadata and
falls back to heuristics, in this order:

1. **schema.org JSON-LD** — `ComicStory`, `ComicIssue`, `Article`, `BlogPosting`,
   `NewsArticle`, `PodcastEpisode`, `CreativeWork`: `name`, `image`,
   `datePublished`, `associatedMedia`.
2. **Open Graph** — `og:title`, `og:image`, `article:published_time`.
3. **HTML link relations** — `<link rel="next">`, `<a rel="next">`, and their `prev`
   counterparts.
4. **Known platform patterns** — ComicControl (Hiveworks), the WordPress comic
   plugins (Webcomic, Toocheke, ComicPress/ComicEasel), common class names.
5. **User overrides** — CSS selectors in the source config. When set, these win.

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
  It re-fetches the last known page to look for a new next link.

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

## Testing

`testdata/sites/<name>/` holds small fixture sites that mimic real platforms:
ComicControl, WordPress + Webcomic, a site with only JSON-LD, a site with a cycle, one
with a missing next link mid-archive, a robots.txt disallow, and one that a test makes
return 429 with `Retry-After`. Tests serve them with `httptest` and use a fake clock
for politeness timing. Their fetcher can only dial loopback, so a test that strays
onto the real network fails.

## Conditional requests

`PageFetcher` has `Fetch`, which is never conditional, and `Revalidate`, which is.
Phase 5 had one method that was always conditional in the crawl loop. A save that
failed left validators in `crawl_log` and no cursor, so the retry got a 304 with no
body for its first page and stopped there as if it had reached the live end. Only a
tail check can use a 304, because only it already holds the page's content.

## Where this diverged from the proposal

- **`Next` takes a `PageFetcher`.** The crawl loop's adds conditional requests and
  `crawl_log` rows, which a probe does not want.
- **`fill_gaps` is the switch for page extraction on feeds,** and `full_text` on a
  feed source works only with it, because each costs a request per entry.
- **An item's kind is decided per item,** so one podcast's bonus text posts stay
  `article` under `default_kind: article`.
- **Redirects are followed by hand, one hop at a time,** so each hop goes through
  robots.txt and the host's politeness. The SSRF guard runs in the dialler, after DNS.
- **JSON-LD uses `json-gold` with an offline loader.** It serves schema.org's context
  from memory and refuses every other URL, because a page must not be able to make
  andante fetch one.
- **`sources.due_at` means "forced" when set.** With none, the loop works out from
  the lookahead whether a source needs indexing. With one, it waits for it: a
  requested crawl, the next tail check, or the end of a backoff.
- **A host's backoff never blocks the loop.** The fetcher refuses without touching the
  network and reports when to come back, which becomes `due_at`.
- **A failing robots.txt backs off** instead of being cached as "disallow all" for a
  day, which RFC 9309 allows but which would stop a source over one bad minute.
- **A batch is at most 10 pages per source per pass,** so one long archive cannot keep
  the others waiting.
- **The content hash covers title and images, not the raw page,** because pages carry
  timestamps and sidebars that change between fetches. A serial that repeats a page
  exactly, title and image, reads as a cycle.
- **Probes can outlive `--write-timeout`,** because they wait out `--crawl-interval`
  between pages. They extend their own deadline, up to `--probe-timeout`.
- **A probe's entries have an `id` of 0,** because they are not saved.
- **`crawl_log` has no pruning yet.** It grows by one row per request.

Read positions reach the loop through `LoopOptions.Furthest`, which `internal/reads`
supplies.
