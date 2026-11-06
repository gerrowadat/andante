# Proposal: Discovery beyond crawling

Status: proposed, not scheduled. What has shipped is in
[`design/discovery.md`](../design/discovery.md).

## Strategies

Later candidates: `webtoons` (episode numbers), `wayback` (rebuild a feed's archive
from Wayback Machine snapshots; see `../prior-art.md`), and a backward crawl via
`rel="prev"` to extend an archive earlier than a nominated start page (negative
`seq`).

## Tail mode

A `crawl` source with a feed could be checked through its feed in tail mode. Today it
checks its last page.

## Mid-archive insertions

Insertions into the middle of an archive are out of scope for v1. Detect them where
cheap (a known page's next link now points to an unknown URL that is not at the end)
and log a warning.
