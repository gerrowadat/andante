# FAQ

## Why Does My Reader Show Today's Date on a Page From 2009?

Because today is when it was released to you. Items carry their release time and not the
original date, so that your reader files them as new. If they said 2009, most readers would
sort them to the bottom or quietly drop them. The original date is in the item's text, with
a link to the original page.

## Why Did Nothing Crawl When I Fetched My Feed?

Feed requests only read the index. They never trigger a crawl, so a popular feed can't end
up hammering some poor publisher's site. The crawl loop gets on with things by itself: it
indexes a source as far as your reads need plus `--crawl-lookahead`, then checks for new
entries every `--tail-interval`. If you can't wait, run `andante source crawl <slug>`. A new
source with no reads only indexes the lookahead.

## Why Isn't My New Source Crawling?

It might be paused. An OPML import creates paused sources, so that you can probe each one
first. `andante source edit <slug> --resume` turns one on. It might also be in `error`, with
the reason in `last_error`, or waiting for a host to allow requests again, in which case
`due_at` says when.

## Why Was a Page Skipped?

andante doesn't skip pages by choice, so this depends on the strategy.

- **`crawl`** follows "next" links, so a page that nothing links to is never seen. A wrong
  next link can also jump over pages. Check with a probe and a `next` selector.
- **`numeric`** treats a 404 as a gap. `max_consecutive_misses` of them in a row are taken
  as the live end, so raise it if the archive has a long hole in it.
- **`feed` and `feed-paged`** only see what the feed holds. They skip an item with no guid,
  link or enclosure, with a warning, and a probe warns when a feed looks cut off.

If a site changes its archive later, insertions in the middle aren't picked up.

andante also stops at pages that robots.txt disallows. It doesn't try to route around them.

## Why Hasn't an Entry Appeared When Its Time Has Passed?

An entry is only released after it's been indexed. If a read catches up with the index, it
waits. A read's `due` and `index_end` will show you this. See [reads](reads.md).
