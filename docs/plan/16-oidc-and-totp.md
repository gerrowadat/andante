# Phase 16: OIDC and TOTP

Read first: `CLAUDE.md`, the login section of `docs/design/web-ui.md`.

## Goal

The login is fit for the public internet.

## Do

- TOTP for password accounts, with hashed single-use recovery codes, and a way for admins to require it.
- OIDC with `coreos/go-oidc`: authorization code flow, PKCE, `state` and `nonce`.
- Users matched on issuer and subject, never on email.
- `--oidc-allowed-domain` and `--oidc-allowed-group`, and invites for everyone else.
- A small fake OIDC provider for tests.

## Acceptance Criteria

- [ ] A provider's email claim can't take over an existing account.
- [ ] A TOTP code can't be used twice.
- [ ] Password login can be turned off.

## Finish

Branch `phase-16-oidc-and-totp`. PR. Stop.
