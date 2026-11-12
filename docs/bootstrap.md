# Bootstrapping

A new andante has no users. This page gets you from a freshly [installed](install.md) server to an
admin who can log in, use the CLI and let other people in. Why it works this way is in
[the design record](design/bootstrap.md).

## The First Admin

While there's no admin, the server logs a warning on every start with the address of its setup
page (`/setup` under the base URL) and a bootstrap token:

```sh
docker compose logs andante | grep bootstrap                   # Compose
nomad alloc logs -stderr -job andante server | grep bootstrap   # Nomad
```

Open the setup page, paste the token and pick a username. It sends you on to set a password, or to
sign in with your OpenID provider if you've [set one up](configuration.md#logging-in-with-a-provider).
That account is the first admin, and you're logged in as it.

A token the server made lasts a day, and restarting the server after that makes a new one. To pick
your own instead, set `--bootstrap-token` (`ANDANTE_BOOTSTRAP_TOKEN`) to at least 32 characters.
`openssl rand -hex 32` makes a good one. Under Nomad, put `bootstrap_token` in the job's Variable
and pass it to the server.

Once there's an admin the token does nothing, and the server logs that `--bootstrap-token` can come
out. The token never takes over an account that's already there, and wrong tokens are throttled
like wrong passwords.

### Without a Browser

The CLI does the same over the API, and prints the new admin's API token and a link to set a
password:

```sh
andante bootstrap dave
```

It reads the token from standard input, so paste it when it waits. Or set it in
`ANDANTE_BOOTSTRAP_TOKEN`, but the Compose and Nomad aliases in [installing](install.md#the-cli)
don't pass that into the container, so pipe it in there instead:

```sh
printf '%s\n' "$BOOTSTRAP_TOKEN" | andante bootstrap dave
```

### From the Database

If you can reach the server's database, `andante-server` can make the admin directly, with the same
`--database-url` as the server. It prints an API token, and `user link` prints a single-use link to
set a password, good for a week:

```sh
andante-server user add dave --admin
andante-server user link dave --base-url https://feeds.example.com
```

## The CLI

Make yourself an API token on the Account page, then:

```sh
export ANDANTE_TOKEN=<your token>
andante whoami
```

[Installing](install.md#the-cli) says where to get the CLI.

## Letting People In

An invite is a link that makes an account. It works once and lasts a week:

```sh
andante invite add alex
```

Leave the name off and add `--uses 5` for a link five people can use, each picking their own name.
There's an Invites page on the website too. With an `http://` base URL the session cookie isn't
`Secure`, which is fine for trying it out and not much else.

Only admins invite people to start with. To let everyone invite their friends, up to five unused
places each:

```sh
andante settings set --who-can-invite users --invite-limit 5
```

Friends who only read along don't need an account at all. A viewer feed link is enough. See
[feeds](feeds.md).

## Before You Put It on the Internet

- Put it behind a proxy that does TLS, use an `https://` base URL, and set `--trusted-proxies`, or
  every login looks like it came from the proxy. See [deploying](deploying.md).
- Consider requiring two-factor for password logins, with `andante settings set
  --require-two-factor` or on the Users page.
- With an OpenID provider, `--password-login=false` turns passwords off once everyone uses it.
- Back up the database and keep `--secret` somewhere separate.

## If the Admin Is Locked Out

This revokes an account's API tokens, ends its sessions, clears its password and two-factor, and
prints a new API token. Follow it with `user link` for a new password:

```sh
andante-server user reset dave
andante-server user link dave --base-url https://feeds.example.com
```

An admin can also turn off another user's two-factor from the Users page, for someone who lost
their phone and their recovery codes.
