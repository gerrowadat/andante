# Phase 7 — Feeds and sharing

Read first: `CLAUDE.md`, `docs/proposals/feeds-and-sharing.md` (all of it),
`docs/proposals/architecture.md` ("Public routes", feed_tokens).

## Goal

Each read can be subscribed to as Atom, RSS 2.0 or JSON Feed — in a feed
reader or a podcast app — through per-person tokens; owners get a "next one
now" control; feeds are cacheable and cheap to serve; reads can be exported as
OPML.

## Do

- API: `GET/POST /reads/{id}/tokens` (role, label; raw token returned once,
  with ready-made Atom, RSS and JSON Feed URLs and a suggested one by kind),
  `DELETE /tokens/{id}`.
- `internal/feed`: an internal item model; a format registry with Atom
  (RFC 4287), RSS 2.0 (with `itunes:` and `podcast:` namespaces) and JSON Feed
  1.1 renderers, following the feeds doc on which source fields are copied
  (show metadata) and which are not (`podcast:guid`, item guids). Prefer an established Go library for serialisation if one
  produces fully conformant output (check `id`, `updated`, `link rel="self"`,
  `xml:base` handling and content escaping); otherwise use `encoding/xml` with
  explicit structs. Record the choice in the design doc.
- Decorators: `image`, `content`, `enclosure`, `origin`, `controls` (owner
  only), each declaring the kinds it applies to; the `Decorator` interface as
  in the feeds doc so the planned `links` decorator drops in later.
- Public routes on the public listener: `GET /f/{file}`, with the handler
  parsing `{token}.{atom|rss|json}` (ServeMux wildcards must be a whole path
  segment, so `GET /f/{token}.atom` panics at registration), and
  `GET` and `POST /f/{token}/next` (confirmation page, CSRF token, appends a
  skip segment, redirects to the newly released page). Unknown and revoked
  tokens → 404. `last_seen_at` updated at most hourly.
- HTTP: `ETag` / `If-None-Match` → 304; `Cache-Control` from the next release
  time, clamped; per-token rate limit with 429 + `Retry-After`; correct
  `Content-Type`s (`application/atom+xml`, `application/rss+xml`,
  `application/feed+json`).
- `POST /api/v1/exports/opml` (required `issue: owner|viewer` and `label`;
  issues a fresh token per read and returns OPML 2.0 of their feed URLs). Raw
  tokens are never stored or recoverable.
- Feed-related metrics from the architecture doc.
- Golden-file tests for all three formats × owner/viewer × kinds (page,
  episode, article — build episode and article entries directly in the test
  database; their discovery arrives in phase 9), round-trip parse with gofeed,
  and a test that viewer output never contains an owner URL.
- `docs/feeds.md`: subscribing in a feed reader or podcast app, sharing with
  friends, revoking, what readers see after a pacing change, the "next one
  now" link.

## Out of scope

The `links` decorator and any discussion feature. Landing pages for share
links. WebSub.

## Acceptance criteria

- [ ] A fixture source with a read and two viewer tokens serves identical items to both viewers and an extra control link to the owner.
- [ ] Item `id`s and timestamps are unchanged after a schedule change and after a skip (tested).
- [ ] 304, 404, 429 and cache headers tested.
- [ ] Output validates as Atom 1.0, RSS 2.0 and JSON Feed 1.1 (golden files reviewed by eye once, then by tests); the RSS episode feed has everything a podcast app needs (enclosure with type and length, `itunes:image`, `itunes:duration`).

## Finish

Branch `phase-7-feeds`. PR. Stop. Move `docs/proposals/feeds-and-sharing.md` to
`docs/design/feeds-and-sharing.md`, leaving the `links` decorator in a
proposal.
