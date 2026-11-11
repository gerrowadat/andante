# Proposal: Bootstrapping the First Admin

Status: proposed, for phase 17 of the [build plan](../plan/README.md).

Today the first admin comes from `andante-server user add --admin` and `andante-server user link`,
which needs a shell with the database URL in it. On Compose that's `docker compose exec`, on Nomad
it's `nomad alloc exec`, and on anything managed it might not exist at all. Single sign-on doesn't
help much, because most people will log in with Google or another provider that has no groups to
hand out an admin role, and an allowed domain of `gmail.com` would let in everyone on earth.

"The first person to log in becomes the admin" is out. It leaves a page on the internet that whoever
finds it first can claim, and [`login.md`](../design/login.md) already says there's no such page.

## The Bootstrap Token

A secret that lets whoever has it make the first admin, and that stops working once there is one.

- `--bootstrap-token` (`ANDANTE_BOOTSTRAP_TOKEN`) sets it. It has to be at least 32 characters,
  and `openssl rand -hex 32` is what the docs will suggest. This suits Nomad with Vault and
  anything else that already hands out secrets.
- If it's not set, and there's no active admin when the server starts, the server makes one. It's
  stored sealed with `--secret`, the same as feed tokens, so that every replica and every restart
  logs the same one. It's logged at warn on each start until it's used, with the address to use it
  at:

  ```
  level=WARN msg="there's no admin yet; go to the setup page and give it the bootstrap token" url=https://feeds.example.com/setup token=...
  ```

- A token from the flag wins, and a stored one is deleted when the flag is set.
- Once there's an active admin, the token does nothing. The stored one is deleted. If the flag is
  still set, the server logs at info that it can come out.

"No active admin" rather than "no users" means a site that let people in by allowed domain before
anyone was made admin can still be set up. It uses `CountActiveAdmins`, which already exists.

## Using It

`GET /setup` is a form with two boxes, the token and a name. `POST /setup` checks the token, and if
it's right and there's still no admin, it makes an admin invite for that name and sends the browser
to `/invite/{token}`. From there it's the normal invite page, which offers a password, single sign-on
or both, depending on `--password-login` and `--oidc-issuer`. Signing in with Google on it links that
Google account to the new admin, and allowed domains don't come into it, the same as any invite.

Reusing the invite means there's no second way to set a password or link an identity to test.

The token isn't put in the logged address as a query string, because it would end up in access logs
and proxies. People paste it into the form.

The name has to be free. The token makes a new account and never takes over an existing one. Anyone
already there can be made an admin afterwards with `PATCH /users/{id}`.

For a site with no web UI in front of it, `POST /api/v1/bootstrap` takes the token and a name and
returns the new admin's API token and a link to set a password, the same as `user add` and
`user link` print now. `andante bootstrap` in the CLI wraps it. Neither needs authentication, because
the token is the authentication.

## Abuse

- The token is compared in constant time, by comparing hashes.
- Both `/setup` and `/api/v1/bootstrap` go through the login throttle for the address (which moves
  out of `internal/web` so that the API can share it), so guessing is
  as slow as guessing a password. A 32-character token can't be guessed anyway, but the throttle
  stops the attempts filling the logs.
- Every attempt goes in the audit log, `bootstrap` or `bootstrap failed`, and is counted in
  `andante_bootstrap_attempts_total{result}`.
- An invite made by the token is marked as a bootstrap invite. Making a new one deletes any unused
  earlier one, and using one fails if an active admin has turned up in the meantime, under the users
  lock. Two people with the token racing each other end up with one admin.
- When there's nobody to set it up, `/setup` is a 404. It doesn't say whether there's an admin.

## What Stays

`andante-server user add`, `user link` and `user reset` stay for recovery and for people who'd
rather use them. The startup warning about there being no users goes, because the bootstrap warning
replaces it.

## Not Doing

- **An admin role from the provider's claims.** Grafana and Gitea map a group claim to an admin role,
  but Google and most consumer providers don't send groups.
- **A password in an environment variable** (Miniflux, Keycloak). It makes a password that lives in
  the environment for ever. The token is used once, and the person picks their own password.
- **Writing the token to a file** (Jenkins, GitLab). In a container nobody reads that file without
  `exec`, which is what this is getting away from.

## Questions

- How long a generated token lasts. See [#51](https://github.com/gerrowadat/andante/issues/51).
