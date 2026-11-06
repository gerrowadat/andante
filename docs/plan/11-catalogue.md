# Phase 11: The Catalogue

Read first: `CLAUDE.md`, `docs/proposals/web-ui.md`, `docs/design/discovery.md`,
`docs/design/architecture.md`.

## Goal

Split andante into `andante-catalogue`, which holds sources and entries and does the crawling, and
`andante-server`, which keeps a copy and owns everything else. `/api/v1` doesn't change in this phase.

## Do

- `internal/catalogue/`: store, migrations, API, `api/catalogue.yaml`, generated client.
- A `revision` on sources and entries, set by trigger, committing in order. `GET /changes`, long polled.
- `PUT` and `DELETE /sources/{id}/demand`, followed by the crawl loop.
- `internal/replica`: client, sync and demand. `available_at` in the copy.
- Packaging, Compose, Nomad and docs for two services.

## Out of Scope

Users, and any change to `/api/v1`.

## Acceptance Criteria

- [ ] Every existing test passes against the two services.
- [ ] The end-to-end suite stops the catalogue and shows feeds and releases carrying on.
- [ ] Revisions are shown to commit in order, under concurrent writes.
- [ ] The Compose stack runs from a locally built image.

## Finish

Branch `phase-11-catalogue`. PR. Stop.
