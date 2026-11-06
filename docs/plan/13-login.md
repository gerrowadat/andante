# Phase 13: Login

Read first: `CLAUDE.md`, the login section of `docs/proposals/web-ui.md`, and the OWASP authentication
and session management cheat sheets.

## Goal

Someone can log in with a password, safely, and get a session. There are no pages beyond login.

## Do

- Argon2id passwords, with a limit on concurrent hashes, and an embedded common-password list.
- Server-side sessions in a `__Host-` cookie, stored hashed, rotated on login and privilege change.
- Single-use invite links.
- `http.CrossOriginProtection`, CSRF tokens, CSP, HSTS and `frame-ancestors`.
- Rate limits by IP and by account, with backoff. `--trusted-proxies`.
- An audit log.

## Out of Scope

OIDC and TOTP. Nothing should face the internet yet.

## Acceptance Criteria

- [ ] Login failures look the same and take the same time, whether or not the user exists.
- [ ] A stranger can't lock out a real user.
- [ ] A session survives a restart, and ends on logout, on expiry, and when revoked.

## Finish

Branch `phase-13-login`. PR. Stop.
