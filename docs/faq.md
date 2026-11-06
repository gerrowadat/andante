# FAQ

## Why does my reader show today's date on a page from 2009?

Because that is when it was released to you. Items carry their release time and not the
original date, so a reader files them as new. If they carried 2009, most readers would
sort them to the bottom or drop them. The original date is in the item's text, with a
link to the original page.

## Why did nothing crawl when I fetched my feed?

Feed requests only read the index. They never trigger a crawl, so a popular feed cannot
hammer a publisher's site. The crawl loop works on its own: it indexes a source as far
as your reads need plus `--crawl-lookahead`, then checks for new entries every
`--tail-interval`. To check now, run `andante source crawl <slug>`. A new source with no
reads indexes only the lookahead.

## Why isn't my new source crawling?

It may be paused: an OPML import creates paused sources, so that you can probe each one
first. Enable one with `andante source edit <slug> --resume`. It may also be in `error`,
with the reason in `last_error`, or be waiting for a host to allow requests again, in which case
`due_at` says when.

## Why was a page skipped?

andante does not skip a page by choice, so it depends on the strategy.

- **`crawl`** follows "next" links, so a page that nothing links to is never seen. A
  wrong next link can also jump over pages. Check with a probe and a `next` selector.
- **`numeric`** treats a 404 as a gap. `max_consecutive_misses` of them in a row are read
  as the live end, so raise it if the archive has a long hole.
- **`feed` and `feed-paged`** see only what the feed holds, and skip an item with no guid,
  link or enclosure, with a warning. A probe warns when a feed looks cut off.

If a site changes its archive later, insertions in the middle are not picked up.

andante also stops at pages that robots.txt disallows and does not route around them.

## Why does an entry not appear even though its time has passed?

An entry is released only after it has been indexed. If a read catches up with the index,
it waits. A read's `due` and `index_end` show this. See [reads](reads.md).
