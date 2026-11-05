# Design philosophy

Why andante behaves the way it does. Component designs live in
[`proposals/`](proposals/) until built and [`design/`](design/) after.

## Pace is the product

andante exists so that a long archive arrives at a human pace. Everything else
— sharing, discovery strategies, output formats — serves that. A feature that
makes bingeing easier by default is the wrong feature.

## Any serial, not just comics

A webcomic, a podcast, a blog and a news site are the same shape: an ordered
archive of entries, each with a date and a link. The core (index, segments,
release arithmetic, tokens, feed serving) treats them identically. What differs
— images versus audio enclosures versus article text — lives at the edges, in
extractors and decorators selected by an entry's kind. A change to the core
that only makes sense for one kind of serial is in the wrong place.

The original run's timing is first-class, not an afterthought: replaying a
podcast's gaps at seven times speed, or reading a blog exactly twenty years
after it was written, is the same machinery as one comic page a day.

## Discovery, release and serving are separate

Discovery fills an index. Release is arithmetic over that index. Serving renders
what release says is due. None of them waits for another: a slow or broken
crawl never breaks a feed, and changing a schedule never needs a re-crawl.

## The feed is a function, not a job

What a read shows is computed from its append-only history (segments) and the
current time. There is no daily job that advances a counter, so there is
nothing to miss, double-run or repair after downtime. A release's timestamp is
recomputed identically on every request, so readers never see items reorder or
reappear.

## Be a good guest on other people's sites

andante reads sites run by individual artists, podcasters and writers. It
honours robots.txt, keeps to one request per host every few seconds by
default, sends conditional requests, identifies itself honestly, backs off on
429/503, and indexes lazily rather than mirroring archives. It links images,
audio and video from where they live, and links every item back to the
original page; it never rehosts anyone's work, and it never downloads
enclosures. Public feed traffic can never cause a crawl.

## Configuration lives in the database

Flags and env vars cover what the process needs to start (where the database
is, what to listen on, the base URL). Everything a user configures — sources,
reads, schedules, tokens — is data, changed through the versioned API. There
are no config files to drift from the database.

## The API is the only way in

The admin API, described by an OpenAPI document, is the single interface for
changing state. The CLI is a client of it, and so will be any UI. Nothing
writes config by reaching into the database. Within a major version the API
only grows; CI enforces this.

## Open standards first

Before inventing a format, protocol or convention, use the open or common one.
Current choices:

| Concern | Standard |
|---|---|
| Output feeds | Atom ([RFC 4287](https://www.rfc-editor.org/rfc/rfc4287)); [RSS 2.0](https://www.rssboard.org/rss-specification) (podcast apps expect it); [JSON Feed 1.1](https://www.jsonfeed.org/version/1.1/) |
| Media | RSS `<enclosure>` / Atom `rel="enclosure"`; Apple Podcasts (`itunes:`) and [Podcasting 2.0](https://podcasting2.org/docs/podcast-namespace) (`podcast:`) tags passed through from the source where present |
| Input feeds | RSS 2.0, Atom, JSON Feed; [RFC 5005](https://www.rfc-editor.org/rfc/rfc5005) archived feeds where a site offers them |
| Feed lists | [OPML 2.0](http://opml.org/spec2.opml) export of your reads (subscribe to all of them in one go), and import of sources |
| Schedules | iCalendar recurrence rules ([RFC 5545](https://www.rfc-editor.org/rfc/rfc5545) RRULE) with an IANA time zone; durations as ISO 8601 (`P7D`, `PT12H`) |
| Timestamps | [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339), UTC at rest |
| Page metadata | schema.org JSON-LD (`ComicStory`, `Article`, `PodcastEpisode`, `CreativeWork`), Open Graph, HTML `rel="next"`/`rel="prev"` — before any CSS selector |
| Crawling | [RFC 9309](https://www.rfc-editor.org/rfc/rfc9309) robots.txt; [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) conditional requests; `Retry-After` |
| Feed serving | ETag / `If-None-Match`, `Cache-Control` ([RFC 9111](https://www.rfc-editor.org/rfc/rfc9111)) |
| API description | OpenAPI (3.1 if the generator fully supports it, else 3.0.3) |
| API errors | Problem Details ([RFC 9457](https://www.rfc-editor.org/rfc/rfc9457)), `application/problem+json` |
| API auth | Bearer tokens ([RFC 6750](https://www.rfc-editor.org/rfc/rfc6750)) |
| API pagination | Opaque cursors, with `Link` headers ([RFC 8288](https://www.rfc-editor.org/rfc/rfc8288)) |
| Metrics | Prometheus exposition format / OpenMetrics |
| Health | `/healthz` (alive) and `/readyz` (can serve) |
| Containers | OCI image + `org.opencontainers.image.*` annotations |
| Versions | [SemVer 2.0](https://semver.org); [Keep a Changelog](https://keepachangelog.com) |
| Licensing | SPDX identifiers in every source file |
| Supply chain | SBOMs (SPDX), SLSA build provenance via GitHub artifact attestations, OpenSSF Scorecard |
| Vulnerability reports | GitHub private vulnerability reporting |

If something here turns out to be wrong for andante, the deviation and its
reasoning go in `docs/design/`.

## Small, and extensible at the seams

v1 is deliberately small: no comments, no accounts, no UI beyond what the CLI
needs. It grows at named seams instead: discovery strategies (`Strategy`),
page extraction (`Extractor`), entry kinds (`page`, `episode`, `article`),
pacing modes (`rrule`, `replay`), feed item content (`Decorator`), output
formats, and per-read options (`reads.config jsonb`). A new feature should fit one of those before it
earns a new one.
