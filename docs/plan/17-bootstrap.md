# Phase 17: Bootstrapping

Read first: `CLAUDE.md`, `docs/design/bootstrap.md`, `docs/design/login.md`. Check the
`maintainer-question` issues linked from the proposal for answers.

## Goal

The first admin can be made from a browser, with no shell on the server.

## Do

- `--bootstrap-token` (`ANDANTE_BOOTSTRAP_TOKEN`), and a generated token stored sealed and logged
  while there's no active admin.
- `GET` and `POST /setup`, making a bootstrap invite and handing over to the invite page.
- `POST /bootstrap` in `api/openapi.yaml`, and `andante bootstrap` in the CLI.
- The login throttle shared with the API, audit lines, and `andante_bootstrap_attempts_total`.
- `docs/configuration.md` and `docs/running.md` (now `docs/bootstrap.md`) lead with the token. Move the proposal to
  `docs/design/`, or fold it into `docs/design/login.md`.

## Acceptance Criteria

- [ ] With an active admin, the token does nothing, from the flag or generated.
- [ ] Two people using the token at once end up with one admin.
- [ ] The token never takes over an existing account.
- [ ] A site with `--no-password-login` and a provider can be set up with the provider alone.

## Finish

Branch `phase-17-bootstrap`. PR. Stop.
