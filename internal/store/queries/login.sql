-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: SetPassword :exec
UPDATE users SET password_hash = $2 WHERE id = $1;

-- name: ClearPassword :exec
UPDATE users SET password_hash = NULL WHERE id = $1;

-- name: InsertSession :one
INSERT INTO sessions (user_id, token_hash, ip, user_agent, created_at, last_seen_at)
VALUES ($1, $2, $3, $4, $5, $5)
RETURNING *;

-- name: GetLiveSession :one
SELECT s.id, s.user_id, s.last_seen_at, u.name, u.role
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
INSERT INTO invites (token_hash, name, role, user_id, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetLiveInvite :one
SELECT * FROM invites WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2;

-- name: GetLiveInviteForUpdate :one
SELECT * FROM invites WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2 FOR UPDATE;

-- name: UseInvite :exec
UPDATE invites SET used_at = $2 WHERE id = $1;

-- name: InsertAudit :exec
INSERT INTO audit_log (at, who, action, target, ip) VALUES ($1, $2, $3, $4, $5);

-- name: ListAudit :many
SELECT * FROM audit_log WHERE id > $1 ORDER BY id LIMIT $2;
