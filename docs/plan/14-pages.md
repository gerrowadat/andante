# Phase 14: Pages

Read first: `CLAUDE.md`, the pages and stack sections of `docs/proposals/web-ui.md`.

## Goal

A logged-in user can do everything the CLI can.

## Do

- Server-rendered Go with `html/template`, embedded static files and vendored htmx. No Node and no
  build step. Every form works without JavaScript.
- Home, Browse, Read, Feed links, Source, Source edit, Users and Account.
- A `/users` API for the Users page: list, disable, enable, delete and change role. Disabling a user
  leaves their feeds running, so that friends on a group read aren't cut off, and an admin can revoke
  the tokens. Deleting a user removes their reads and tokens, and asks first, because it can't be
  undone and a read can't be handed on (#40).
- Browse shows when each source was last checked, because a source nobody reads is parked (#41).
- Phase 12 only added users from the command line. Changing a role ends that user's sessions, which phase 13 couldn't do yet.
- Tracking a source from Browse: pick a pace, get a feed link.
- The Add page, moved here from phase 15, which built what's under it. Paste an address and run
  `POST /discoveries` in the background, showing progress. Go to the source if there's an
  `existing_source_id`, ask before adding one with `similar_source_ids`, and fall back to asking for the
  first page's URL.
- A feed page at `GET /f/{token}` on the public listener, with no login: the source, the pace in
  words, the position and the next release. Add the pace to the feed's description too (#30).
- Pace presets, using the CLI's shorthand, moved to a shared package.

## Out of Scope

OIDC and TOTP.

## Acceptance Criteria

- [ ] A test on every route shows one user can't reach another's data.
- [ ] Every page works with JavaScript off.
- [ ] Browse never calls the catalogue.
- [ ] The feed page shows nothing the feed URL doesn't already grant, and a revoked token gets a 404.

## Finish

Branch `phase-14-pages`. PR. Stop.
