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
`openssl rand -hex 32` makes a good one. Under Nomad, put `bootstrap_token` in the job's Variable,
and the example job passes it to the server.

Once there's an admin the token does nothing, and the server logs that `--bootstrap-token` can come
out. The server is happy with it set to nothing, so in a templated deployment blanking the value is
the safe way to do that. The example Nomad job copes with the key being deleted too, but if your
own template reads it without a `{{ with }}`, deleting the key breaks the next deploy, which can be
weeks later. The token never takes over an account that's already there, and wrong tokens are throttled
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

## Password Rules

A new password needs 15 characters to start with, and nothing about which characters. That's NIST
SP 800-63B's advice for a password that can be the only thing standing between someone and the
account. Passwords on a list of common ones, ones with the person's name in and ones that are one
character over and over are always refused.

To change it, on the Users page or with the CLI:

```sh
andante settings set --password-min-length 12 --password-classes 3
```

The minimum length is 8 to 128. Classes is how many of lower-case letters, capitals, digits and
anything else a password has to have, 0 to 4. With two-factor required, 8 characters is enough by
NIST's reckoning. Composition rules mostly get you `Password1!`, so I'd leave classes at 0 unless
something outside andante insists on them.

The rules apply to passwords set from then on. Nobody's existing password stops working, so to make
everyone meet a new rule, give them each a new password link.

Friends who only read along don't need an account at all. A viewer feed link is enough. See
[feeds](feeds.md).

## Before You Put It on the Internet

- Put it behind a proxy that does TLS, use an `https://` base URL, and set `--trusted-proxies`, or
  every login looks like it came from the proxy. See [deploying](deploying.md).
- Consider requiring two-factor for password logins, with `andante settings set
  --require-two-factor` or on the Users page.
- Check the [password rules](#password-rules) suit you.
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
