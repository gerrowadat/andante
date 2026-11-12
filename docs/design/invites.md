# Design: Invites That Let a Site Grow

Status: built in phase 18 of the [build plan](../plan/README.md).

Only an admin could invite anyone, with `POST /invites`, and each invite was for one person, used
once, with a name the admin picked. That's fine for a household. It's a faff for a site where a few
people want to bring their friends, and with Google as the provider there's no allowed domain to fall
back on.

There's still no public sign-up. Every account traces back to someone who was let in. Users being able
to invite at all was asked in [#52](https://github.com/gerrowadat/andante/issues/52), and the answer
was to build it, off by default.

## Who Can Invite

`who_can_invite` is a site setting in the `settings` table, changed with `PATCH /settings` or on the
Users page, next to the setting that requires two-factor:

- `admins`, the default, is how it was.
- `users` lets anyone with an account invite people. What they invite is always a `user`, never an
  admin, and they can't make reset links. Asking for either is a 403.

An invite only works while whoever made it could still make it. A user's links stop working if
they're disabled, or if the setting goes back to `admins`, and start again if it comes back. Deleting
a user deletes their invites. Invites the server makes itself (the bootstrap invite, and
`andante-server user link`) have no maker and aren't affected.

## Links for More Than One Person

An invite has `max_uses`, 1 by default and at most 100, and an expiry, a week by default and at most
30 days. `name` is optional. Without one, whoever uses the link picks their own name on the invite
page before setting a password or going to single sign-on. A name or a reset is for one person, so
asking for more uses with either is a 400. A one-person invite with a name works as it did.

Uses are recorded in `invite_uses`, one row per use with who it made, in place of the single
`used_at`. The migration copies the old `used_at` values across. Using an invite takes the users lock
as before, then holds the invite's row and counts its uses, so people arriving at once can't take more
places than the link has. A store test sends ten people at a link for three.

## Keeping Track

- `users.invited_by` is the name of whoever made the invite, kept as a name like the audit log so
  that it outlives the inviter. It's in `GET /users`, `andante user list` and the Users page, so an
  admin can find a whole branch and disable it with the `PATCH /users/{id}` that's already there.
  Lobsters does the same with its invite tree.
- `GET /invites` lists invites with uses left: your own, or everyone's for an admin, with how many
  have been used and who made each. The links aren't in it, because only their hashes are stored.
- `DELETE /invites/{id}` withdraws one. It deletes the row, which drops its uses but not `invited_by`
  on the accounts it made. Someone else's is a 404. A user can withdraw their own links even after
  the setting has gone back to `admins`.
- The website has an Invites page for both, shown in the menu to anyone who can invite.
  `andante invite list` and `andante invite rm` do the same from the command line.

## Limits

A user can have at most `invite_limit` unused places out at once, counting each use left on each live
link, so a link for three takes three places. It's a second setting, 5 by default. Admins have no
limit. Making an invite holds the user's row while it counts, so two made at once can't both squeeze
under the limit. Going over is a 409.

`andante_invite_uses_total{result}` counts invites used, and refused when the link was used up,
withdrawn or out of date.

## Where It Diverged

- The API's `Invite` keeps `name` required, and it's empty for an invite without one. Making it
  optional or nullable would have been a breaking change to `/api/v1`.
- Listing needed a new schema, `InviteSummary`, because `Invite.url` is required and only exists when
  the invite is made.
- Picking a name is a step of its own on the invite page. Single sign-on is a link, and a name typed
  next to a password field would have ended up in the link's query string along with the password.
- The proposal didn't say what happens to a user's invites when they lose the right to make them.
  They stop working, as above.

## Not Doing

- **Open sign-up, or sign-up with approval** (Mastodon). It's a queue for an admin to look after, and
  the page it needs is the unclaimed one andante doesn't have.
- **Email.** There's still nothing to send mail with, so people pass the link on themselves.
