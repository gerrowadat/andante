# Phase 15: Adding a Site

Read first: `CLAUDE.md`, the discovery and duplicates sections of `docs/proposals/web-ui.md`.

## Goal

Paste a site's address and get a working source, or be told it already exists.

## Do

- `POST /discoveries` on the catalogue, using the same fetcher as probes.
- Canonical keys with a unique index, and a 409 from `POST /sources` with the existing source's ID.
- Matching on title and home page, and the UI asking before adding one that looks close.
- The Add page, which runs discovery in the background and shows progress.
- Falling back to asking for the first page's URL.

## Acceptance Criteria

- [ ] Fixture sites cover a full feed, a cut-off feed, a WordPress site and a comic with no feed.
- [ ] Adding a serial twice gives one source, and no requests to the site the second time.
- [ ] Discovery keeps to robots.txt and `--crawl-interval`.

## Finish

Branch `phase-15-add-a-site`. PR. Stop.
