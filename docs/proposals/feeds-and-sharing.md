# Proposal: Feeds and sharing

Status: proposed. Covers phase 7 of the [build plan](../plan/README.md).

## Output formats

Every read is available as:

- **Atom** (RFC 4287), `GET /f/{token}.atom` — the default for feed readers.
- **RSS 2.0**, `GET /f/{token}.rss` — what podcast apps expect; includes
  `<enclosure>`, the `itunes:` channel and item tags (`itunes:image`,
  `itunes:duration`, `itunes:episode`, `itunes:explicit`, …) and
  Podcasting 2.0 `podcast:` tags (`podcast:transcript`, `podcast:chapters`,
  `podcast:guid`) passed through from the source where present.
- **JSON Feed 1.1**, `GET /f/{token}.json` — with `attachments` for
  enclosures.

All three are rendered from the same internal item model; formats are a small
registry so another can be added without touching the rest.

Token creation returns all three URLs, and suggests one based on the source's
`default_kind` (RSS for episodes, Atom otherwise). The feed contains the most
recent `window` released entries (default 30, per-read option in
`reads.config`).

Feed-level fields: title `"{source title} — {read name}"`, `id` a stable tag
URI per read, `updated` = the latest release time, `link rel="self"` to the
feed URL, `link rel="alternate"` to the source's home page, the source's
artwork as the feed image/logo, `generator` andante plus version. The
description says plainly that this is a paced replay and links to the
original, so a shared feed never passes itself off as the publisher's.

Podcast-specific channel tags (`itunes:author`, `itunes:category`, …) are
copied from the source feed into `sources.extra` so apps display the show
properly. The feed's
`podcast:guid` is **not** copied: it identifies the original show, and a
replay is a different feed. Item `guid`s are andante's own for the same
reason (see below), with the original guid kept in `extra`.

Item-level fields:

- `id`: `tag:{base-url host},{year}:read/{read_id}/entry/{entry_id}` — stable
  across schedule changes, unique per read so two reads of one serial do not
  collide in a reader.
- `published` and `updated`: the entry's **release time**, never the original
  publication date (otherwise readers file items in 2009). `updated` does not
  change when the entry's metadata is refreshed; changing it makes readers
  resurface the item.
- `title`: the entry title, else `"{source title} #{seq}"`.
- `link rel="alternate"`: the original page, omitted if the entry has no URL.
- `content type="html"`: built by decorators.

## Decorators

```go
type ItemContext struct {
    Read  Read
    Entry Entry
    Role  Role // owner | viewer
}

type Decorator interface {
    Decorate(ctx ItemContext, item *Item)
}
```

Items pass through an ordered list. v1 ships:

1. `image` (kind `page`) — the image(s), hotlinked, never proxied, with alt
   text shown underneath as text, not only in the `title` attribute (most
   readers do not show it).
2. `content` (kind `article`, and any entry with content) — the stored,
   sanitised content, else the summary plus a "read on the original site"
   link.
3. `enclosure` (kind `episode`, and any entry with enclosures) — enclosures as
   format-native elements (RSS `<enclosure>`, Atom `rel="enclosure"`, JSON
   Feed `attachments`), plus show notes from content or summary. Media URLs
   are passed through unchanged.
4. `origin` — "Originally published {date}" when known, and a link to the
   original page.
5. `controls` — owner role only: a "next one now →" link to
   `/f/{token}/next`.

Decorators declare which kinds they apply to; the core never switches on kind.

Planned, not v1: `links` — per-read link templates from `reads.config`, e.g.
`{"links": [{"label": "Discuss", "url": "https://forum.example/t/{source}-{seq}"}]}`
with `{seq}`, `{title}`, `{url}`, `{source}` substituted. This is the
"discuss it somewhere else" feature, kept outside andante.

## Tokens and sharing

- A read has one or more **feed tokens**. Role `owner` gets the feed plus
  controls; role `viewer` gets the feed only.
- Give each person their own viewer token with a label, so one can be revoked
  without breaking the others. `last_seen_at` (updated at most once an hour per
  token to keep writes down) shows who is still reading.
- A group read is just a read with several viewer tokens. Everyone sees the
  same entries at the same moment because the feed is computed from the
  segments.
- Feeds are rendered per role, and the feed URL is the only credential. A viewer
  feed never contains an owner link.
- Tokens are 32 random bytes, base64url, stored hashed (SHA-256 is fine for
  high-entropy tokens; there is nothing to brute-force).

## The "next one now" control

`GET /f/{token}/next` shows a small confirmation page (one button). `POST`
appends a `skip` segment of 1 and redirects to the newly released entry's
original page, or the source's home page if the entry has no URL. It is two
steps because feed readers and link previewers
prefetch GET links. Basic CSRF protection: the form carries a short-lived token
derived from the feed token.

## HTTP behaviour

- `ETag` = hash of (read id, released position, latest segment id, role,
  format). `If-None-Match` gets a 304.
- `Content-Type`: `application/atom+xml`, `application/rss+xml`,
  `application/feed+json`.
- `Cache-Control: public, max-age=N` where N is the time until the next
  release, clamped to [60 s, 1 h].
- Per-token rate limit (default 60 requests/hour, a flag), 429 with
  `Retry-After` beyond it.
- Unknown or revoked token → 404.

## OPML

`POST /api/v1/exports/opml` takes a required `issue` (`owner` or `viewer`) and
`label`, issues a fresh token per read with that role and label, and returns an
OPML 2.0 document of their feed URLs, ready to import into Miniflux or any
reader. Raw tokens are never stored or recoverable, so an export can only
contain tokens created for it, and it is a `POST` because it creates them. Each
export adds tokens; revoke an old file's tokens by their label. A `viewer`
export can be handed to a friend.
