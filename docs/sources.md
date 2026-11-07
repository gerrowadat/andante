# Sources

A source is a serial, plus how to find its archive.

## Adding a Site

Give andante the site's address, and it works out the rest:

```sh
andante source discover https://comic.example/
```

It looks for a feed the page links to, then tries common paths such as `/feed` and `/rss.xml`, and
checks whether the feed holds the whole archive. If it doesn't, it looks for a `?paged=` feed, or a
link to the first page. Each candidate gets a normal probe, so you can see what you'd get before you
add anything. Best first, each one has the config to pass to `source add`. Nothing is saved.

This keeps to the same limits as crawling, so it can take a minute or two. If it finds nothing, it
says so, and you'll need to find the first page yourself and use a probe.

A serial is only crawled once, so if the catalogue already has this one it says which source, without
asking the site anything. Track that one with a read. A candidate can also list sources that look close,
with the same title or home page. Have a look at those before adding another.

## Choosing a Strategy

Start with what the serial's feed holds, because a feed costs one request and a crawl costs
one per page. Most podcasts and many blogs keep their whole archive in their feed. Most
webcomics don't.

| The serial has | Use | Config |
|---|---|---|
| A feed with the whole archive, or RFC 5005 archived feeds | `feed` | `feed_url`, optional `fill_gaps`, `full_text` |
| A WordPress-style feed that takes `?paged=N` | `feed-paged` | `feed_url`, optional `page_param`, `max_pages`, `fill_gaps`, `full_text` |
| URLs with an incrementing number | `numeric` | `url_template` with `{n}`, `start`, optional `max_consecutive_misses`, `format`, `fields` |
| Pages with a "next" link | `crawl` | `start_url`, optional `selectors`, `full_text` |

A probe of a `feed` warns when the feed looks cut off at a round number of items. If it is,
the rest of the archive isn't in the feed, so use `feed-paged` or `crawl`.

In a `numeric` source a 404 is a gap, and `max_consecutive_misses` of them in a row (5 by
default) are taken as the live end. With `format: json`, `fields` says where each value is,
with dots for nested keys:

```sh
andante source probe 'https://xkcd.example/{n}/info.0.json' --strategy numeric --start-id 1 --format json \
  --field title=safe_title --field image=img --field alt_text=alt --field year=year --field month=month --field day=day
```

`default_kind` sets the entries' kind, but an item with an audio or video enclosure is always
an `episode`. An entry from a feed is identified by its guid, because podcast items often
share a link. Enclosures are passed through and never rehosted. Their type and length come
from the feed or the page, with a `HEAD` request only when those are missing.

`fill_gaps` fetches each item's page for whatever the item lacks, such as a comic image.
That's one more request per entry, so it's off by default.

## Full Text

Full-text extraction of articles is off by default, and `full_text` turns it on for one
source. If a publisher's feed only gives a summary, that was the publisher's choice, so
andante shows the summary and links to the original. Comic images and podcast enclosures are
different: they're what every visitor gets, and andante links to them rather than copying
them.

On a `feed` source, `full_text` needs `fill_gaps`, because the text comes from the linked
page. Anything stored from a page or a feed goes through an HTML sanitiser first, which
removes scripts, event handlers and iframes.

## Importing OPML

```sh
andante import opml feeds.opml
```

This creates a paused `feed` source for each outline, including those in folders, so nothing
crawls until you've probed one and enabled it. The result lists each outline as `created`,
`duplicate` (the file or the database already has it) or `invalid`. One bad outline doesn't
stop the rest.

Creating a read on one of these starts it. That only works on a source that has never been crawled,
because one that has was paused on purpose, and only an admin can restart that. The read needs a
schedule, because replay needs dates and nothing is indexed yet.

## Adding a Comic

Probe first. A probe tries a config and saves nothing:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/api/v1/probes \
  -d '{"config": {"strategy": "crawl", "start_url": "https://comic.example/1"}}'
```

It returns the first pages, with `warnings` and `dated_share`, which is the share of pages
with an original date (replay pacing needs those). Probes keep to the same limits as
crawling, so 10 pages take about 45 seconds by default.

When that looks right, create the source with the same config:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8080/api/v1/sources \
  -d '{"slug": "example-comic", "title": "Example Comic", "home_url": "https://comic.example/",
       "default_kind": "page", "config": {"strategy": "crawl", "start_url": "https://comic.example/1"}}'
```

## What the Warnings Mean

| Warning | What it means |
|---|---|
| `no image found` | Nothing on the page looks like the comic. Add an `image` selector. |
| `next link points to the current page` | Normal on a comic's last page. Elsewhere, the `next` selector needs fixing. |
| `no next link on …` | andante treats this page as the live end. If the archive carries on past it, add a `next` selector. |
| `image … is on N of M pages` | That image is probably a site logo and not the comic. Add an `image` selector. |
| `a JSON-LD block …` | The page's metadata could not be read. andante fell back to the page itself. Contexts other than schema.org are refused, so a page cannot make andante fetch a URL. |
| `stopped early: …` | The probe got some pages and then the site stopped answering. |

## Selector Overrides

andante looks for open metadata first: schema.org JSON-LD, then Open Graph, then link
relations, then the markup of common comic platforms. When that picks the wrong thing, set
CSS selectors in the source's config. If they're set, they win:

```json
{
  "strategy": "crawl",
  "start_url": "https://comic.example/1",
  "selectors": {
    "title": "h1.comic-title",
    "image": "#strip img",
    "alt_text": "#strip img",
    "date": "time.posted",
    "next": "a.forward"
  }
}
```

`alt_text` reads the element's `title` attribute, or else its text. `date` reads a `datetime`
attribute, or else its text. Selectors are checked when the config is written.

## How Indexing Works

`andante-catalogue` does the crawling, so the flags here are its own. It indexes lazily, and a serial
is only crawled once, however many reads it has. It keeps a source `--crawl-lookahead` entries ahead
of the furthest read on it, and a source with no reads gets its first `--crawl-lookahead` entries.
`POST /sources/{id}/crawls` asks for a pass now.

| State | Meaning |
|---|---|
| `indexing` | Working through the archive. |
| `tail` | At the live end, checking for new pages every `--tail-interval` while someone reads it. With no reads it's parked: nothing checks it until a read starts, and then it's checked at once. Returns to `indexing` when a new page appears. |
| `paused` | Left alone. Set it with `PATCH`, and set `indexing` to resume. |
| `error` | Stopped, with the reason in `last_error`. Fix the cause, then set `indexing` with `PATCH`. |

A source goes to `error` when robots.txt disallows a page, when its links form a cycle, or
when a page can't be used. A site that's failing or rate limiting isn't an error: the source
waits, with the reason in `last_error`, and tries again when `due_at` comes.

If a known page's title or image changes when it's re-fetched, the entry is updated in place.
Its `seq` never changes.

## Politeness

| Rule | Setting |
|---|---|
| robots.txt is honoured, and cached per host | `--robots-ttl` (24 h) |
| One request in flight per host, spaced apart | `--crawl-interval` (5 s) |
| 429 and 503 honour `Retry-After`; other 5xx back off exponentially | `--crawl-backoff-cap` (6 h) |
| Requests are conditional where a page was seen before | |
| The `User-Agent` names andante, its version and this instance | `--user-agent` |
| Redirects stop after 5 hops, and each hop obeys all of the above | |
| Response bodies are limited | `--crawl-max-body` (5 MiB) |
| Private and loopback addresses are refused, after DNS and on every redirect | `--crawl-allow-private` |

Feed requests never trigger a crawl.
