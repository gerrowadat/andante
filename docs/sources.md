# Sources

A source is a serial, plus how to discover its archive. So far andante can add
image-led serials such as webcomics, using the `crawl` strategy, which walks pages by
their "next" link.

## Adding a comic

Probe first. A probe tries a config and saves nothing:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8081/api/v1/probes \
  -d '{"config": {"strategy": "crawl", "start_url": "https://comic.example/1"}}'
```

It returns the first pages, with `warnings` and `dated_share`, the share of pages with
an original date, which replay pacing needs. Probes keep to the same limits as
crawling, so 10 pages take about 45 seconds by default.

When that looks right, create the source with the same config:

```sh
curl -X POST -H "Authorization: Bearer $ANDANTE_TOKEN" -H 'Content-Type: application/json' \
  http://127.0.0.1:8081/api/v1/sources \
  -d '{"slug": "example-comic", "title": "Example Comic", "home_url": "https://comic.example/",
       "default_kind": "page", "config": {"strategy": "crawl", "start_url": "https://comic.example/1"}}'
```

## What the warnings mean

| Warning | What it means |
|---|---|
| `no image found` | Nothing on the page looks like the comic. Add an `image` selector. |
| `next link points to the current page` | Normal on a comic's last page. Elsewhere, the `next` selector needs fixing. |
| `no next link on …` | andante treats this page as the live end. If the archive carries on past it, add a `next` selector. |
| `image … is on N of M pages` | That image is probably a site logo and not the comic. Add an `image` selector. |
| `a JSON-LD block …` | The page's metadata could not be read. andante fell back to the page itself. Contexts other than schema.org are refused, so a page cannot make andante fetch a URL. |
| `stopped early: …` | The probe got some pages and then the site stopped answering. |

## Selector overrides

andante looks for open metadata first: schema.org JSON-LD, then Open Graph, then link
relations, then the markup of common comic platforms. When that picks the wrong thing,
set CSS selectors in the source's config. When set, they win:

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

`alt_text` reads the element's `title` attribute, or else its text. `date` reads a
`datetime` attribute, or else its text. Selectors are checked when the config is
written.

## How indexing works

andante indexes lazily. It keeps a source `--crawl-lookahead` entries ahead of the
furthest read on it, and a source with no reads gets its first `--crawl-lookahead`
entries. `POST /sources/{id}/crawls` asks for a pass now.

| State | Meaning |
|---|---|
| `indexing` | Working through the archive. |
| `tail` | At the live end, checking for new pages every `--tail-interval`. Returns to `indexing` when a new page appears. |
| `paused` | Left alone. Set it with `PATCH`, and set `indexing` to resume. |
| `error` | Stopped, with the reason in `last_error`. Fix the cause, then set `indexing` with `PATCH`. |

A source goes to `error` when robots.txt disallows a page, when its links form a cycle,
or when a page cannot be used. A site that is failing or rate limiting is not an error:
the source waits, with the reason in `last_error`, and tries again when `due_at` comes.

If a known page's title or image changes when it is re-fetched, the entry is updated in
place. Its `seq` never changes.

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
