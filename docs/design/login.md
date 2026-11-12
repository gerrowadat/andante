# Design: Login

Passwords, sessions and invites shipped in phase 13 of the [build plan](../plan/README.md), and
two-factor and OpenID Connect in phase 16, following the OWASP cheat sheets and NIST SP 800-63B. How
to use it is in [`running.md`](../running.md) and [`configuration.md`](../configuration.md).

There's no public sign-up, so there's never an unclaimed page on the internet. A provider's allowed
domains and groups are the nearest thing to one.

## Accounts

The first admin comes from the bootstrap token at `/setup` (see [bootstrap.md](bootstrap.md)), or
from `andante-server user add --admin` and `andante-server user link` on the command line. After that, people come in on invite links from `POST /invites`. A link
lasts a week unless it says otherwise, and it's only ever stored hashed. Links for more than one
person, and users inviting people, are in [invites.md](invites.md). There's no email, so someone has to pass the link on
themselves.

A link is also how a password gets reset, with `reset`. Using one ends all of that user's sessions.
`andante-server user reset` is for an account that's lost or compromised: it revokes API tokens, ends
sessions and clears the password.

## Passwords

Argon2id, 64 MiB and 3 passes, in the PHC string format, so that the cost can go up later without
breaking old hashes. A hash costs 64 MiB, so only two run at once, and a login waits up to 10 seconds for
a turn and then gets a 503. Passwords are 12 to 128 characters with no composition rules. They're
normalised to NFC, so that the same password typed on a different keyboard still matches.

The common-password list is small and hand-written, because only passwords of 12 characters or more
matter here. It could do with a bigger one.

## Failing the Same Way

A login with the wrong password, an unknown name, no password set or a disabled user all give the same
page and do one hash. An account that has no hash to check spends the time on a dummy, which is what
`Hasher.Waste` is for. Tests count the hashes, because timing a test is a good way to get a flaky one.

## Throttling

Rate limits by account are how a stranger locks someone out, so there are three limits:

| Limit | Free tries | Then |
|---|---|---|
| An address | 20, because it can be a whole office | Doubling from 2 seconds, up to 15 minutes |
| An address on one account | 3 | The same |
| An account, from anywhere | 20 | One a minute |

The first two only ever touch the address doing the guessing. The third is for guessing from a lot of
addresses, and it only delays, by about a minute. An address the user has had a session from in the last
30 days skips it, so a stranger can't keep them out from where they normally log in.

Someone logging in from a new address while a botnet works through their account can still lose the
race for the one try a minute. That's the trade for not having a lockout.

State is in memory, so it starts again with the server, and a second replica has its own.
`--trusted-proxies` says whose `X-Forwarded-For` to believe, and the header is walked from the right,
because the left side is whatever the client said.

## Sessions

A random 256-bit ID in a `__Host-andante_session` cookie (`Secure`, `HttpOnly`, `SameSite=Lax`), stored as
a hash so that a database leak can't be replayed. They last 7 days idle and 30 days at most. Expiry is
worked out from two timestamps, so no job has to clean up. A login always makes a new session and ends
the one it was given, so an ID planted before login is no use after it.

Over plain HTTP the cookie can't be `Secure`, and `__Host-` needs that, so with an `http://` base URL it's
called `andante_session`. That's for development.

Users list and end their own sessions through the API, and an admin can't end someone else's.

## Requests

`http.CrossOriginProtection` from the standard library covers every POST, including login, where there's
no session for a CSRF token to come from. Logged-in forms also carry a token derived from the session, so
there's nothing to store and it's dead when the session is. Pages send a strict Content-Security-Policy,
`frame-ancestors 'none'`, `nosniff`, `no-referrer` and `no-store`, and HSTS over HTTPS.

## Two-Factor

TOTP (RFC 6238) for password logins, with `pquerna/otp`, six digits, 30 seconds and a step either
side for a phone whose clock is out. The secret is sealed with `--secret`, the same as a feed token's
copy, because checking a code needs the secret itself. Losing `--secret` turns two-factor off for
everyone.

Each user has the last step a code was taken for, and a code is only taken for a later step, in one
`UPDATE`. So a code can't be used twice, even by two requests at once, and nor can an older one that's
still in the window. The code that turns two-factor on uses up its step too.

Setting it up makes ten recovery codes of 80 random bits each. They're stored as SHA-256 hashes,
because they're random and a slow hash buys nothing, and each is marked used in one `UPDATE`.

Between the password and the code, the browser holds a cookie sealed with `--secret` that says who
got their password right, good for 10 minutes. Nothing is stored, and the session only exists once
the code is right. A wrong code counts against the same throttle as a wrong password, so guessing a
million codes goes no faster than guessing a password.

An admin can require two-factor. Someone logged in with a password who hasn't set it up is sent to
set it up, and every other page refuses them until they have. That's checked on every request, in
the same query that finds the session, so turning it on takes effect at once. It doesn't touch API
tokens, which are their own credential.

## OpenID Connect

The authorization code flow with PKCE, `state` and `nonce`, using `coreos/go-oidc` and
`golang.org/x/oauth2`. The state, nonce and PKCE verifier go in a sealed cookie, the same as the
second step of a password login, and the callback checks the state against it. A callback with a code
someone else got is refused, because the cookie isn't there.

A user is matched on the ID token's issuer and subject, never on email or name, because those are
whatever the provider says and linking on them is how accounts get taken over. Someone the provider
logs in who isn't linked to anyone gets a new account if their email is verified and in an allowed
domain, or they're in an allowed group. The new account takes a name from `preferred_username` or the
email, with a number on the end if that's taken. It never joins an existing account with that name.

Linking an existing user goes through an invite: the invite page offers single sign-on, and using it
links the identity instead of setting a password. A reset invite does the same for someone who
already has an account.

The provider is found the first time someone logs in with it, so the server starts even when the
provider is down. Two-factor for these logins is the provider's job, and requiring it here doesn't
apply to them.

Tests use a fake provider in `internal/oidctest`, about 200 lines on `go-jose`. It checks PKCE and the
client secret, and can be told to send the wrong nonce. `mockoidc` exists, but writing one was less
work than finding out how much of PKCE it checks.

## The Audit Log

Logins, failed logins, logouts, invites, user changes, and API and feed tokens all go in an append-only
table, which admins read with `GET /audit`. It holds a name and not a key, so that a line outlives the
user. A name that doesn't exist is written as `unknown`, because people type their password into the
username box.

## Where This Diverged From the Proposal

- **A change of privilege ends the sessions instead of rotating them.** Phase 13 couldn't change roles.
  Phase 14 added that, and changing someone's role, or disabling them, ends all their sessions. See
  [`pages.md`](pages.md).
- **`andante-server user link` is new.** `user add` makes a user with no password, and they needed some
  way to get one.
- **`user reset` clears the password as well,** so a compromised password can't be used again.
- **The throttle per account lets known addresses through.** The proposal said backoff in place of a
  lockout, but a backoff on an account alone still lets a stranger keep someone out.
- **The "next one now" page gets a Content-Security-Policy with no `form-action`,** because browsers apply
  it to the redirect to the publisher's site.
- **There's a small page at `/`,** which says who you're logged in as and has a logout button, because a
  login has to land somewhere.
- **Requiring two-factor is one setting for the site,** in a `settings` table, changed with
  `PATCH /settings`. Requiring it per user would mean an admin keeping a list up to date.
- **An admin can turn off someone's two-factor,** for a lost phone and lost codes. An admin can already
  make a reset link, so this gives them nothing new.
- **There's no page for linking a provider to an account you're logged in to.** A reset invite does
  it, and saves a second way in to test.
- **Groups come from the ID token only,** not from the userinfo endpoint.
