# Proposal: Invites That Let a Site Grow

Status: proposed, for phase 18 of the [build plan](../plan/README.md).

Only an admin can invite anyone now, with `POST /invites`. Each invite is for one person, used once,
with a name the admin picked. That's fine for a household. It's a faff for a site where a few people
want to bring their friends, and with Google as the provider there's no allowed domain to fall back on.

There's still no public sign-up. Every account traces back to someone who was let in.

## Who Can Invite

A site setting, `who_can_invite`, in the `settings` table and changed with `PATCH /settings`, next to the
setting that requires two-factor:

- `admins`, the default, is how it is now.
- `users` lets anyone with an account invite people. What they invite is always a `user`, never an
  admin, and they can't make reset links.

## Links for More Than One Person

An invite gets `max_uses` (1 by default) and an expiry (a week by default, up to 30 days). The
person using it picks their own name on the invite page, so `name` is optional when making one. A
one-person invite with a name works as it does now. Uses are counted in a new `invite_uses` table
in place of the single `used_at`, so that each one records who it made.

## Keeping Track

- `users.invited_by` says whose invite made the account, kept as a name like the audit log so that it
  outlives the inviter. If someone's invitees turn out to be trouble, an admin can see all of them
  (Lobsters does the same with its invite tree).
- `GET /invites` lists your own invites, or everyone's for an admin, with how many uses are left.
  `DELETE /invites/{id}` withdraws one.
- The users page shows who invited whom, so an admin can find and disable a whole branch with the
  `PATCH /users/{id}` that's already there.

## Limits

A user can have at most a set number of unused invite places outstanding, counting each use left on
each link. It's a second setting, `invite_limit`. Admins have no limit.

## Not Doing

- **Open sign-up, or sign-up with approval** (Mastodon). It's a queue for an admin to look after, and
  the page it needs is the unclaimed one andante doesn't have.
- **Email.** There's still nothing to send mail with, so people pass the link on themselves.

## Questions

- Whether users should be able to invite at all, and what the limit should be. See [#52](https://github.com/gerrowadat/andante/issues/52).
