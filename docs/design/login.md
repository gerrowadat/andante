# Design: Login

Shipped in phase 13 of the [build plan](../plan/README.md), following the OWASP cheat sheets and
NIST SP 800-63B. TOTP and OIDC are still in the [proposal](../proposals/web-ui.md), and this shouldn't
face the internet until they land. How to use it is in [`running.md`](../running.md).

`internal/web` serves `/`, `/login`, `/logout` and `/invite/{token}` on the public listener. There are no
other pages yet. There's no public sign-up, so there's never an unclaimed page on the internet.

## Accounts

The first admin comes from `andante-server user add --admin`, and `andante-server user link` prints a
link for them to set a password. After that, an admin makes a single-use link with `POST /invites`. It
lasts a week, and it's only ever stored hashed. There's no email, so someone has to pass the link on
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

## The Audit Log

Logins, failed logins, logouts, invites, user changes, and API and feed tokens all go in an append-only
table, which admins read with `GET /audit`. It holds a name and not a key, so that a line outlives the
user. A name that doesn't exist is written as `unknown`, because people type their password into the
username box.

## Where This Diverged From the Proposal

- **Rotating on a change of privilege isn't built,** because nothing can change a role yet. The role is
  read on every request, so a change takes effect at once. Phase 14 adds changing roles, and rotates then.
- **`andante-server user link` is new.** `user add` makes a user with no password, and they needed some
  way to get one.
- **`user reset` clears the password as well,** so a compromised password can't be used again.
- **The throttle per account lets known addresses through.** The proposal said backoff in place of a
  lockout, but a backoff on an account alone still lets a stranger keep someone out.
- **The "next one now" page gets a Content-Security-Policy with no `form-action`,** because browsers apply
  it to the redirect to the publisher's site.
- **There's a small page at `/`,** which says who you're logged in as and has a logout button, because a
  login has to land somewhere.
