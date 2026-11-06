# Phase 14: Pages

Read first: `CLAUDE.md`, the pages and stack sections of `docs/proposals/web-ui.md`.

## Goal

A logged-in user can do everything the CLI can, except adding a new site by its address.

## Do

- Server-rendered Go with `html/template`, embedded static files and vendored htmx. No Node and no
  build step. Every form works without JavaScript.
- Home, Browse, Read, Feed links, Source, Source edit, Users and Account.
- Tracking a source from Browse: pick a pace, get a feed link.
- Pace presets, using the CLI's shorthand, moved to a shared package.

## Out of Scope

The add flow, OIDC and TOTP.

## Acceptance Criteria

- [ ] A test on every route shows one user can't reach another's data.
- [ ] Every page works with JavaScript off.
- [ ] Browse never calls the catalogue.

## Finish

Branch `phase-14-pages`. PR. Stop.
