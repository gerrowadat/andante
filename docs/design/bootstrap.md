# Design: Bootstrapping the First Admin

Status: built in phase 17 of the [build plan](../plan/README.md).

The first admin used to come from `andante-server user add --admin` and `andante-server user link`,
which need a shell with the database URL in it. On Compose that's `docker compose exec`, on Nomad
it's `nomad alloc exec`, and on anything managed it might not exist at all. Single sign-on doesn't
help much, because most people will log in with Google or another provider that has no groups to
hand out an admin role, and an allowed domain of `gmail.com` would let in everyone on earth.

"The first person to log in becomes the admin" is out. It leaves a page on the internet that whoever
finds it first can claim, and [`login.md`](login.md) already says there's no such page.

## The Bootstrap Token

A secret that lets whoever has it make the first admin, and that stops working once there is one.
The code is in `internal/bootstrap`, with the storage in `internal/store/bootstrap.go`.

- `--bootstrap-token` (`ANDANTE_BOOTSTRAP_TOKEN`) sets it. It has to be at least 32 characters,
  and the docs suggest `openssl rand -hex 32`. This suits Nomad with Vault and anything else that
  already hands out secrets. It doesn't run out.
- If it's not set, and there's no active admin when the server starts, the server makes one. It's
  stored sealed with `--secret`, the same as feed tokens, so that every replica and every restart
  logs the same one. It's logged at warn on each start until it's used, with the address to use it
  at and when it runs out:

  ```
  level=WARN msg="there's no admin yet; go to the setup page and give it the bootstrap token" url=https://feeds.example.com/setup token=... until=2026-11-12T23:52:04Z
  ```

- A generated token lasts 24 hours ([#51](https://github.com/gerrowadat/andante/issues/51)). Logs
  often go somewhere more people can read than can reach the database, and GitLab deletes its
  initial root password after a day for the same reason. The next start after it runs out makes a
  new one and logs that, so a restart is how you get another.
- A token from the flag wins, and a stored one is deleted when the flag is set.
- Once there's an active admin, the token does nothing. The stored one is deleted, by the next start
  or by the bootstrap itself. If the flag is still set, the server logs at info that it can come out.
- `rotate-secret` deletes a stored token rather than reseal it, since the next start makes another.

"No active admin" rather than "no users" means a site that let people in by allowed domain before
anyone was made admin can still be set up.

The token is checked when it's used, against the flag or the stored row, and not against a copy
made at startup. That way a replica that started before another one made a new token still takes
the right one.

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
`user link` print. It makes the admin straight away, with no invite in between. `andante bootstrap
<name>` in the CLI wraps it, and reads the token from `ANDANTE_BOOTSTRAP_TOKEN` or standard input
so that it stays out of process listings. Neither needs an API token, because the bootstrap token is
the authentication.

## Abuse

- The token is compared in constant time, by comparing hashes.
- Both `/setup` and `/api/v1/bootstrap` go through the login throttle for the address, so guessing
  is as slow as guessing a password. The throttle moved out of `internal/web` to `internal/throttle`,
  and the server makes one that the website and the API share. A 32-character token can't be guessed
  anyway, but the throttle stops the attempts filling the logs. The API answers a throttled try with
  a 429 and `Retry-After`.
- Every try with a token goes in the audit log as `bootstrap` or `bootstrap.failed`, by
  `bootstrap token`, with the name as the target. Every try is counted in
  `andante_bootstrap_attempts_total{result}`, where the result is `ok`, `wrong`, `throttled`,
  `closed` (nothing to set up) or `refused` (the right token, but the name was taken).
- An invite made by the token is marked as a bootstrap invite. Making a new one deletes any unused
  earlier one, and using one fails if an active admin has turned up in the meantime. Making the
  invite, making the admin over the API and using any invite all take the users lock first, so two
  people with the token racing each other end up with one admin.
- When there's nobody to set it up, `/setup` and `POST /bootstrap` are a 404. They don't say whether
  there's an admin.

## What Stays

`andante-server user add`, `user link` and `user reset` stay for recovery and for people who'd
rather use them. The startup warning about there being no users went, because the bootstrap warning
replaces it.

## Not Doing

- **An admin role from the provider's claims.** Grafana and Gitea map a group claim to an admin role,
  but Google and most consumer providers don't send groups.
- **A password in an environment variable** (Miniflux, Keycloak). It makes a password that lives in
  the environment for ever. The token is used once, and the person picks their own password.
- **Writing the token to a file** (Jenkins, GitLab). In a container nobody reads that file without
  `exec`, which is what this is getting away from.
