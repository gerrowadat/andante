# Phase 18: Invites

Read first: `CLAUDE.md`, `docs/proposals/invites.md`, `docs/design/login.md`. Check the
`maintainer-question` issues linked from the proposal for answers.

## Goal

A site can grow by people inviting their friends, if its admin wants that.

## Do

- `who_can_invite` and `invite_limit` in `settings`.
- `max_uses` on invites, the name picked on the invite page, and an `invite_uses` table.
- `users.invited_by`, `GET /invites`, `DELETE /invites/{id}`, all additive in `/api/v1`.
- An invites page on the website, and who invited whom on the users page.

## Acceptance Criteria

- [ ] An invite from a user can only make a `user`, and can't reset anyone.
- [ ] A link used by more people than `max_uses` refuses the extra ones, including when they arrive
      at once.
- [ ] A user can't go over `invite_limit`.

## Finish

Branch `phase-18-invites`. PR. Stop.
