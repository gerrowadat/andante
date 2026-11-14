# Design: Discovery

Shipped in phases 5 and 9 of the [build plan](../plan/README.md). What's still unbuilt is in the
[proposal](../proposals/discovery.md). How to add a serial is in [`sources.md`](../sources.md).

Discovery builds and maintains the ordered index of a source's entries. It's the only part of
andante that talks to other people's websites.

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

A cursor is strategy-owned JSON, kept in `sources.cursor` so that a crawl resumes after a
restart.

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

### Entries From Feeds

The feed item is the primary record. An entry's identity is its guid, because podcast
items often share a link or have none, and its `url` is the link, falling back to the
enclosure URL. An item with none of these is skipped with a warning. Namespaced fields
(`itunes:duration`, `itunes:episode`, `itunes:season`, `itunes:image`,
`podcast:transcript`, `podcast:chapters`) go to `extra`. Show metadata goes to
`sources.extra`, and artwork to `sources.image_url`. The kind is `default_kind`,
overridden to `episode` by an audio or video enclosure. Feed content goes through the
same sanitiser as page content, because a feed is no more trusted than a page.

### What Came From FeedRewind

FeedRewind solves the same problem for blogs, so phase 9 studied it. andante took its
feed-first order and its warning for a feed cut off at a round number. It did not
take the heuristics that find archive pages with generated XPaths: `crawl` with
selectors covers those sites, with rules that a person can read.

## Finding a Site

`internal/find` turns a site's address into candidate sources. It uses the probes' fetcher, so
robots.txt, spacing and the SSRF guard all apply. In order, it:

1. Checks the address against each source's home page and canonical key, and stops there if one
   matches. That's what stops a second person adding a serial from costing the site anything.
2. Fetches the address. If that's a feed, it's used as it is.
3. Follows `<link rel="alternate">` feeds, then tries common paths such as `/feed` and `/rss.xml`.
4. If a feed looks cut off, tries `?paged=2`. A different first item means it's paged.
5. If no feed gets the whole archive, looks for a first page to crawl from: `rel="first"`, the
   platform patterns the extractors already know, or a link that says "First". A crawl ranks above a feed that's cut off.

Each idea then gets a normal probe, and one that fails is dropped, so a broken feed can't hide a crawl
that would work. A paged feed's probe stops after two pages, because walking the whole of one here
would take as long as indexing it. The config on the candidate has no such limit.

Each step is reported as it starts, for the Add page to show, and `POST /discoveries` streams them
when asked to. See [Discovery Stages](pages.md#discovery-stages).

### Duplicates

Each source has a canonical key: its strategy plus its normalised feed URL, start URL or URL template
(lower-case host, no default port, no fragment, no trailing slash, query in order). It has a unique
index, and `POST /sources` answers 409 with the existing source's ID, which also covers the CLI and
OPML import. Changing a config to one in use gets the same.

This won't catch everything. The same comic through its feed and by crawling gets two keys, and so does
one feed read as `feed` and `feed-paged`. A site that moves gets a new key. So a candidate also lists
sources with the same title or home page in `similar_source_ids`. Those are for a person to look at,
because neither proves anything.

## Extraction

An `Extractor` pulls fields out of a fetched page. It's chosen by the entry's kind: `page` puts
images first, `article` main text, and `episode` media. It prefers open metadata and falls back
to heuristics, in this order:

1. **schema.org JSON-LD**: `ComicStory`, `ComicIssue`, `Article`, `BlogPosting`,
   `NewsArticle`, `PodcastEpisode`, `CreativeWork`: `name`, `image`,
   `datePublished`, `associatedMedia`.
2. **Open Graph**: `og:title`, `og:image`, `article:published_time`.
3. **HTML link relations**: `<link rel="next">`, `<a rel="next">`, and their `prev`
   counterparts.
4. **Known platform patterns**: ComicControl (Hiveworks), the WordPress comic
   plugins (Webcomic, Toocheke, ComicPress/ComicEasel), common class names.
5. **What a person reads**: a link on the same site labelled "Next", "Previous" or "First"
   (its text, else its `title` or `aria-label`, else the `alt` of an image inside it), and, for
   `page` only, the first image whose class, id or path contains "comic". Images in `header`,
   `footer` and `nav`, and button images inside those links, don't count.
6. **User overrides**: CSS selectors in the source config. If set, these win.

The labels have to match whole, once lower-cased and stripped of punctuation, so "Next »"
counts and "Next week: the finale" doesn't. Arrows on their own don't count, because "»" is
next on one site and last on another. A lot of hand-built comic sites have no metadata at all
and draw their navigation as images with alt text, which is what step 5 is for. Picking the
biggest image would also work for some of them, but that means fetching every image, so it
isn't done.

The extractor returns warnings along with fields ("no image found", "next link
points to the current page", "image is a site logo shared by 90% of pages").
Probes show these warnings, so a bad config gets caught before saving.

`article` also knows WordPress and Ghost post navigation, and `episode` looks at `<audio>` and
`<video>` sources. With `full_text` on, `article` runs an established Go port of Mozilla
Readability and sanitises the result before storing it.

Alt text matters for some comics (xkcd), so `alt_text` is a first-class field: the `title` attribute of the comic image, or the JSON field in
`numeric` JSON mode.

## Politeness

- robots.txt is fetched per host, cached for 24 h and honoured (RFC 9309, via
  an established parser). A disallowed page puts the source in `error` with a
  clear message rather than being fetched.
- At most one request in flight per host, with at least `--crawl-interval`
  (default 5 s) between requests to the same host.
- Conditional requests use `If-None-Match` and `If-Modified-Since` from `crawl_log`.
- 429/503 honour `Retry-After`; other 5xx back off exponentially, capped at 6 h.
- `User-Agent` names andante, its version and the instance's base URL.
- Redirects are followed up to 5 hops and only over http(s). Response bodies
  are size-limited (default 5 MiB). Private and loopback addresses are refused
  unless explicitly allowed (SSRF guard: a source URL is user input).

## Lazy Indexing

The crawl loop picks the source most in need of work:

- A source is **due** when its demand, the furthest read position the server has reported, is
  within `lookahead` (default 50) entries of the end of its index, or when it isn't paused and has
  no demand yet (index the first `lookahead`).
- A source in **tail mode** is due every `tail_interval` (default 6 h, a flag), as long as someone
  reads it. With no demand it parks: its next check isn't booked, so it costs the publisher nothing.
  Demand from a new read books a check at once, and the check picks up whatever was published in
  the meantime (#41). A requested crawl still checks a parked source.
  It re-fetches the last known page to look for a new next link.

The crawl scheduler records this in `sources.due_at`;
`POST /api/v1/sources/{id}/crawls` sets it to now.

A source enters tail mode when the strategy reports the live end: no next link,
a next link pointing back to the same page, or N consecutive misses
(`numeric`). It leaves tail mode when new entries appear.

## Loop and Content-Change Protection

- A content hash per entry detects a "next" chain that cycles back to a page
  already indexed; the crawl stops and the source goes to `error`.
- If a known page's title or image changes on re-fetch, the entry is updated in
  place. `seq` never changes once assigned.

## Testing

`testdata/sites/<name>/` holds small fixture sites that mimic real platforms:
ComicControl, WordPress + Webcomic, a site with only JSON-LD, one with no metadata and
buttons drawn as images, a site with a cycle, one with a missing next link mid-archive, a
robots.txt disallow, and one that a test makes return 429 with `Retry-After`. Tests serve them with `httptest` and use a fake clock
for politeness timing. Their fetcher can only dial loopback, so a test that strays
onto the real network fails.

## Conditional Requests

`PageFetcher` has `Fetch`, which is never conditional, and `Revalidate`, which is.
Phase 5 had one method that was always conditional in the crawl loop. A save that
failed left validators in `crawl_log` and no cursor, so the retry got a 304 with no
body for its first page and stopped there as if it had reached the live end. Only a
tail check can use a 304, because only it already holds the page's content.

## Where This Diverged From the Proposal

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
- **`crawl_log` has no pruning yet.** It grows by one row per request, and a few per pass for
  events. Only the page bodies are bounded, at one per source.

Read positions reach the loop through `LoopOptions.Furthest`, which reads the demand the server
reported. See [architecture](architecture.md#demand-flows-up).
