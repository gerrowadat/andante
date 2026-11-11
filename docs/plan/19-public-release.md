# Phase 19: Going Public

Read first: `CLAUDE.md`, `docs/design/`, `docs/philosophy.md`.

## Goal

The whole thing can be released and put on the internet.

## Do

- Compose and Nomad updates, metrics for logins and sessions, and alerts.
- Rewrite `docs/philosophy.md` where it says there are no accounts and no UI. Move
  `docs/proposals/web-ui.md` to `docs/design/`. Use the dave-prose skill for all of it.
- An end-to-end scenario from login to a working feed.
- A security review of every route, written up in the PR.

## Acceptance Criteria

- [ ] `make e2e` passes locally and in a `release.yml` dispatch with `publish=false`.
- [ ] Every proposal that shipped is in `docs/design/`.
- [ ] The security review's findings are fixed or filed.

## Finish

Branch `phase-19-public-release`. PR. Stop.
