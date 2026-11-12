# Phase 12: Users

Read first: `CLAUDE.md`, `docs/design/web-ui.md`, `docs/design/architecture.md`.

## Goal

Everything people own belongs to a user, and the CLI works as a user.

## Do

- `users`, with roles `admin` and `user`. `andante-server user add --admin` and `user reset`.
- `user_id` with a foreign key on reads and tokens. A store API that can't fetch a read without
  saying whose it is.
- Personal API tokens. Remove `--admin-token`.
- Rewrite `/api/v1` around users, and move it to the main listener. `--ops-listen` replaces
  `--admin-listen` and keeps health and metrics. `GET /sources` gains `?q=` and `?kind=`.
- Feed tokens stored encrypted as well as hashed, under `--secret`, so that a URL can be shown again.
  A command to re-encrypt.
- Update the CLI and its docs.

This turned out to be additive, so `spec-diff` needed no exception.

## Out of Scope

Password login and pages. Authentication here is API tokens only.

## Acceptance Criteria

- [ ] A test on every route shows one user can't reach another's reads or tokens.
- [ ] A feed URL can be shown again, however it was issued.
- [ ] A lost `--secret` fails clearly, and doesn't serve garbage.

## Finish

Branch `phase-12-users`. PR. Stop.
