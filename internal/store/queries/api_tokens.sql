-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: LockAPITokens :exec
SELECT pg_advisory_xact_lock($1);

-- name: CountActiveAPITokens :one
SELECT count(*) FROM api_tokens WHERE user_id = $1 AND revoked_at IS NULL;

-- name: InsertAPIToken :one
INSERT INTO api_tokens (user_id, token_hash, label) VALUES ($1, $2, $3) RETURNING *;

-- name: GetActiveAPITokenByHash :one
SELECT t.id, t.token_hash, u.id AS user_id, u.role, u.name
FROM api_tokens t JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND t.revoked_at IS NULL AND u.disabled_at IS NULL;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = $1 AND user_id = $2;

-- name: ListAPITokens :many
SELECT * FROM api_tokens WHERE user_id = $1 AND id > $2 ORDER BY id LIMIT $3;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllAPITokens :exec
UPDATE api_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');
