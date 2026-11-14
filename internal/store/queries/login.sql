-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: SetPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: ClearPassword :exec
UPDATE users SET password_hash = NULL WHERE id = $1;

-- name: InsertSession :one
INSERT INTO sessions (user_id, token_hash, ip, user_agent, created_at, last_seen_at, method)
VALUES ($1, $2, $3, $4, $5, $5, $6)
RETURNING *;

-- name: GetLiveSession :one
SELECT s.id, s.user_id, s.last_seen_at, s.method, u.name, u.role,
       (u.password_hash IS NOT NULL)::boolean AS has_password, (u.totp_enabled_at IS NOT NULL)::boolean AS has_totp,
       coalesce((SELECT require_totp FROM settings), false)::boolean AS require_totp,
       coalesce((SELECT who_can_invite FROM settings), 'admins')::text AS who_can_invite
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash) AND s.revoked_at IS NULL AND u.disabled_at IS NULL
  AND s.created_at > sqlc.arg(created_after) AND s.last_seen_at > sqlc.arg(seen_after);

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = $2 WHERE id = $1;

-- name: ListLiveSessions :many
SELECT * FROM sessions
WHERE user_id = sqlc.arg(user_id) AND id > sqlc.arg(after) AND revoked_at IS NULL
  AND created_at > sqlc.arg(created_after) AND last_seen_at > sqlc.arg(seen_after)
ORDER BY id LIMIT sqlc.arg(page_size);

-- name: CountLiveSessions :many
SELECT s.method, count(*)::bigint AS sessions
FROM sessions s JOIN users u ON u.id = s.user_id
WHERE s.revoked_at IS NULL AND u.disabled_at IS NULL
  AND s.created_at > sqlc.arg(created_after) AND s.last_seen_at > sqlc.arg(seen_after)
GROUP BY s.method;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = coalesce(revoked_at, sqlc.arg(at))
WHERE id = sqlc.arg(id) AND user_id = sqlc.arg(user_id);

-- name: RevokeSessionByHash :exec
UPDATE sessions SET revoked_at = coalesce(revoked_at, sqlc.arg(at)) WHERE token_hash = sqlc.arg(token_hash);

-- name: RevokeAllSessions :exec
UPDATE sessions SET revoked_at = sqlc.arg(at) WHERE user_id = sqlc.arg(user_id) AND revoked_at IS NULL;

-- name: HasLoggedInFrom :one
SELECT EXISTS (
    SELECT 1 FROM sessions WHERE user_id = sqlc.arg(user_id) AND ip = sqlc.arg(ip) AND created_at > sqlc.arg(since)
);

-- name: InsertInvite :one
INSERT INTO invites (token_hash, name, role, user_id, created_at, expires_at, max_uses, created_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetInvite :one
-- An invite, with what decides whether it still works: how often it's been used, who
-- made it and whether they still can.
SELECT sqlc.embed(i), (SELECT count(*) FROM invite_uses u WHERE u.invite_id = i.id)::integer AS uses,
       c.name AS creator_name, c.role AS creator_role, (c.disabled_at IS NOT NULL)::boolean AS creator_disabled,
       coalesce((SELECT who_can_invite FROM settings), 'admins')::text AS who_can_invite
FROM invites i LEFT JOIN users c ON c.id = i.created_by
WHERE i.token_hash = sqlc.arg(token_hash) AND i.expires_at > sqlc.arg(now);

-- name: LockInvite :exec
SELECT 1 FROM invites WHERE token_hash = $1 FOR UPDATE;

-- name: UseInvite :exec
INSERT INTO invite_uses (invite_id, user_id, used_at) VALUES ($1, $2, $3);

-- name: InsertInvitedUser :one
INSERT INTO users (name, role, invited_by) VALUES ($1, $2, $3) RETURNING *;

-- name: ListInvites :many
-- Invites with uses left, everyone's or one user's.
SELECT sqlc.embed(i), c.name AS creator_name,
       (SELECT count(*) FROM invite_uses u WHERE u.invite_id = i.id)::integer AS uses
FROM invites i LEFT JOIN users c ON c.id = i.created_by
WHERE i.id > sqlc.arg(after) AND i.expires_at > sqlc.arg(now) AND NOT i.bootstrap
  AND (sqlc.arg(everyone)::boolean OR i.created_by = sqlc.arg(user_id))
  AND (SELECT count(*) FROM invite_uses u WHERE u.invite_id = i.id) < i.max_uses
ORDER BY i.id LIMIT sqlc.arg(page_size);

-- name: DeleteInvite :execrows
DELETE FROM invites WHERE id = sqlc.arg(id) AND (sqlc.arg(everyone)::boolean OR created_by = sqlc.arg(user_id));

-- name: CountInvitePlaces :one
-- How many more people a user's live invites could let in.
SELECT coalesce(sum(i.max_uses - (SELECT count(*) FROM invite_uses u WHERE u.invite_id = i.id)), 0)::integer
FROM invites i WHERE i.created_by = $1 AND i.expires_at > $2;

-- name: GetInviteSettings :one
SELECT coalesce((SELECT who_can_invite FROM settings), 'admins')::text AS who_can_invite,
       coalesce((SELECT invite_limit FROM settings), 5)::integer AS invite_limit;

-- name: SetInviteSettings :exec
INSERT INTO settings (who_can_invite, invite_limit)
VALUES (coalesce(sqlc.narg(who_can_invite)::text, 'admins'), coalesce(sqlc.narg(invite_limit)::integer, 5))
ON CONFLICT (only_row) DO UPDATE SET
    who_can_invite = coalesce(sqlc.narg(who_can_invite)::text, settings.who_can_invite),
    invite_limit = coalesce(sqlc.narg(invite_limit)::integer, settings.invite_limit);

-- name: GetPasswordPolicy :one
SELECT coalesce((SELECT password_min_length FROM settings), 8)::integer AS password_min_length,
       coalesce((SELECT password_non_letters FROM settings), 1)::integer AS password_non_letters,
       coalesce((SELECT password_classes FROM settings), 0)::integer AS password_classes;

-- name: SetPasswordPolicy :exec
-- The store has already checked the whole policy, so this writes all of it.
INSERT INTO settings (password_min_length, password_non_letters, password_classes)
VALUES (sqlc.arg(password_min_length)::integer, sqlc.arg(password_non_letters)::integer, sqlc.arg(password_classes)::integer)
ON CONFLICT (only_row) DO UPDATE SET
    password_min_length = excluded.password_min_length,
    password_non_letters = excluded.password_non_letters,
    password_classes = excluded.password_classes;

-- name: GetTheme :one
SELECT coalesce((SELECT theme FROM settings), 'default')::text AS theme;

-- name: SetTheme :exec
INSERT INTO settings (theme) VALUES (sqlc.arg(theme)::text)
ON CONFLICT (only_row) DO UPDATE SET theme = excluded.theme;

-- name: InsertAudit :exec
INSERT INTO audit_log (at, who, action, target, ip) VALUES ($1, $2, $3, $4, $5);

-- name: ListAudit :many
SELECT * FROM audit_log WHERE id > $1 ORDER BY id LIMIT $2;

-- name: RevokeOtherSessions :exec
UPDATE sessions SET revoked_at = sqlc.arg(at)
WHERE user_id = sqlc.arg(user_id) AND id <> sqlc.arg(keep) AND revoked_at IS NULL;
