# Proposal: Discovery beyond crawling

Status: proposed. Covers phase 9 of the [build plan](../plan/README.md). What has
shipped is in [`design/discovery.md`](../design/discovery.md).

## Strategies

Which one fits depends on the serial more than its kind: most podcasts and many
blogs keep their whole archive in their feed (`feed`), most webcomics do not
(`crawl` or `numeric`), and WordPress sites of every kind can be walked with
`feed-paged`.

| Strategy | Use when | Config |
|---|---|---|
| `numeric` | URLs carry an incrementing ID. | `url_template` (`{n}`), `start`, `max_consecutive_misses`, optional `format: html\|json` |
| `feed` | The feed holds the whole archive, or the site publishes RFC 5005 archived feeds (`rel="prev-archive"`). | `feed_url` |
| `feed-paged` | WordPress-style feeds that accept `?paged=N`. | `feed_url`, `max_pages` |

Later candidates (not v1): `webtoons` (episode numbers), `wayback` (rebuild a feed's
archive from Wayback Machine snapshots; see `../prior-art.md`), and a backward crawl
via `rel="prev"` to extend an archive earlier than a nominated start page (negative
`seq`).

For `feed` and `feed-paged`, entries are ordered by published date and given `seq`
values after the full walk, oldest first.

## Entries from feeds

When entries come from a feed (`feed`, `feed-paged`), the feed item is the primary
record: title, link, guid, date, summary, full content (`content:encoded`, Atom
`content`), enclosures, and namespaced fields (`itunes:duration`, `itunes:episode`,
`itunes:season`, `itunes:image`, `podcast:transcript`, `podcast:chapters`) kept in
`extra` for pass-through. An entry's identity is its guid where it has one, because
podcast items often share a link or have none; its `url` is the item's link, falling
back to the enclosure URL. An item with no guid, link or enclosure is skipped with a
warning. Show and site metadata (author, categories, `itunes:` channel fields) is
copied to `sources.extra`, and artwork to `sources.image_url`. The entry's kind comes
from the source's `default_kind`, overridden to `episode` when an item has an audio
or video enclosure. Page extraction runs on the linked page only to fill gaps (a
comic image a feed item lacks, the full text of an article whose feed is summary-only
— off by default for articles, see below).

## Extraction

The `article` extractor puts main text first, and `episode` puts media first. Beyond
what `page` does, they add:

- **Platform patterns** — for articles, WordPress and Ghost post navigation
  (`.nav-next a`, `rel="next"` in post footers); for episodes, `<audio>`/`<video>`
  sources.
- **Readability** (articles only) — main-content extraction with an established Go
  port of Mozilla Readability, sanitised with an HTML sanitiser before storage.

Full-text extraction of articles is **off by default** and opt-in per source. Where a
publisher's feed gives only a summary, the default is to show the summary and link to
the original — the publisher chose that. Comic images and podcast enclosures are
different: they are what the publisher put in front of every visitor, and andante
links to them rather than copying them.

## Tail mode

A source with a feed is also checked through its feed in tail mode.

## Mid-archive insertions

Insertions into the middle of an archive are out of scope for v1. Detect them where
cheap (a known page's next link now points to an unknown URL that is not at the end)
and log a warning.

## Testing

Fixture sites still to add: a numeric-ID site, a JSON-API site in the style of xkcd, a
podcast feed with `itunes:`/`podcast:` tags and the full archive, a summary-only blog
feed, a WordPress blog crawlable by post navigation, and a paged news feed.
