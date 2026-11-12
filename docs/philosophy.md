# Design Philosophy

Why andante behaves the way it does. Component designs live in
[`proposals/`](proposals/) until they're built, and [`design/`](design/) after.

## Pace Is the Product

andante exists so that a long archive arrives at a human pace. Everything else (sharing,
discovery strategies, output formats) is there to serve that. A feature that makes
bingeing easier by default is the wrong feature.

## Any Serial, Not Just Comics

A webcomic, a podcast, a blog and a news site are all the same shape: an ordered archive of
entries, each with a date and a link. The core (index, segments, release arithmetic, tokens,
feed serving) treats them identically. What differs (images, audio enclosures, article text) lives at the
edges, in extractors and decorators selected by an entry's kind.
If a change to the core only makes sense for one kind of serial, it's in the wrong place.

The original run's timing is first-class. Replaying a podcast's gaps at
seven times speed, or reading a blog exactly twenty years after it was written, is the same
machinery as one comic page a day.

## Discovery, Release and Serving Are Separate

Discovery fills an index. Release is arithmetic over that index. Serving renders what release
says is due. None of them waits for another, so a slow or broken crawl never breaks a feed,
and changing a schedule never needs a re-crawl.

## The Feed Is a Function, Not a Job

What a read shows is computed from its append-only history (segments) and the current time.
There's no daily job advancing a counter, so there's nothing to miss, double-run or repair
after downtime. A release's timestamp is recomputed identically on every request, so readers
never see items reorder or reappear.

## Be a Good Guest on Other People's Sites

andante reads sites run by individual artists, podcasters and writers, so it honours robots.txt, keeps to one request per host every few seconds by
default, sends conditional requests, says honestly who it is, backs off on 429/503, and
indexes lazily rather than mirroring archives. It links images, audio and video from where
they live, and links every item back to the original page. It never rehosts anyone's work,
and it never downloads enclosures. Public feed traffic can never cause a crawl.

## Configuration Lives in the Database

Flags and env vars cover what the process needs to start (where the database is, what to
listen on, the base URL). Everything a user configures (sources, reads, schedules, tokens)
is data, changed through the versioned API. There are no config files to drift from the
database.

## The API Is the Only Way In

The API, described by an OpenAPI document, is the single interface for changing state.
The CLI is a client of it. The website runs in the same process as the API, and calls the same
operations code (`internal/app`) as its handlers, so it can't do anything the API can't. Nothing
writes config by reaching into the database. Within a major version the API only grows, and CI enforces this.

## Open Standards First

Before inventing a format, protocol or convention, use the open or common one. Current
choices:

| Concern | Standard |
|---|---|
| Output feeds | Atom ([RFC 4287](https://www.rfc-editor.org/rfc/rfc4287)); [RSS 2.0](https://www.rssboard.org/rss-specification) (podcast apps expect it); [JSON Feed 1.1](https://www.jsonfeed.org/version/1.1/) |
| Media | RSS `<enclosure>` / Atom `rel="enclosure"`; Apple Podcasts (`itunes:`) and [Podcasting 2.0](https://podcasting2.org/docs/podcast-namespace) (`podcast:`) tags passed through from the source where present |
| Input feeds | RSS 2.0, Atom, JSON Feed; [RFC 5005](https://www.rfc-editor.org/rfc/rfc5005) archived feeds where a site offers them |
| Feed lists | [OPML 2.0](http://opml.org/spec2.opml) export of your reads (subscribe to all of them in one go), and import of sources |
| Schedules | iCalendar recurrence rules ([RFC 5545](https://www.rfc-editor.org/rfc/rfc5545) RRULE) with an IANA time zone; durations as ISO 8601 (`P7D`, `PT12H`) |
| Timestamps | [RFC 3339](https://www.rfc-editor.org/rfc/rfc3339), UTC at rest |
| Page metadata | schema.org JSON-LD (`ComicStory`, `Article`, `PodcastEpisode`, `CreativeWork`), Open Graph, HTML `rel="next"`/`rel="prev"`, before any CSS selector |
| Crawling | [RFC 9309](https://www.rfc-editor.org/rfc/rfc9309) robots.txt; [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) conditional requests; `Retry-After` |
| Feed serving | ETag / `If-None-Match`, `Cache-Control` ([RFC 9111](https://www.rfc-editor.org/rfc/rfc9111)) |
| API description | OpenAPI 3.1 |
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

If one of these turns out to be wrong for andante, the deviation and the reasoning go in
`docs/design/`.

## Sources Are Shared, Reads Are Not

andante has users so that several people can share one server and each have their own reads. A
serial is crawled once however many people read it, which is kinder to the site. Your reads, and
how far through them you are, are yours. Other users don't see them, though an admin can. You share a read by
handing out a viewer link, and whoever gets it needs no account.

The website is there so that nobody needs the CLI to get a feed. It does what the API does and no
more (see above). There's no public sign-up: people come in on an invite, or through an OpenID
provider you've told andante to trust.

## Safe on the Internet

andante is meant to be put on the public internet behind a TLS proxy, and anything that isn't safe
there (metrics, health checks, the catalogue) is on a listener of its own. Logins follow the OWASP
cheat sheets and NIST SP 800-63B rather than anything invented here. See
[`design/login.md`](design/login.md).

## Small, and Extensible at the Seams

v1 is small on purpose: no comments, no public sign-up, and accounts only so that people can have their own reads. It grows at
named seams instead: discovery strategies (`Strategy`), page extraction (`Extractor`), entry
kinds (`page`, `episode`, `article`), pacing modes (`rrule`, `replay`), feed item content
(`Decorator`), output formats, and per-read options (`reads.config jsonb`). A new feature
should fit one of those before it earns a new one.
