# Phase 15: Adding a Site

Read first: `CLAUDE.md`, the discovery and duplicates sections of `docs/design/web-ui.md`.

## Goal

Paste a site's address and get a working source, or be told it already exists.

## Do

- `POST /discoveries` on the catalogue, using the same fetcher as probes.
- Canonical keys with a unique index, and a 409 from `POST /sources` with the existing source's ID.
- Matching on title and home page, with candidates marked as looking close.
- `andante source discover`, and the same operation on `/api/v1`.

The Add page moved to phase 14, because this phase went first.

## Acceptance Criteria

- [ ] Fixture sites cover a full feed, a cut-off feed, a WordPress site and a comic with no feed.
- [ ] Adding a serial twice gives one source, and no requests to the site the second time.
- [ ] Discovery keeps to robots.txt and `--crawl-interval`.

## Finish

Branch `phase-15-add-a-site`. PR. Stop.
