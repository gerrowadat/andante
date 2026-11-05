-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: LockAPITokenBootstrap :exec
SELECT pg_advisory_xact_lock($1);

-- name: CountAPITokens :one
SELECT count(*) FROM api_tokens;

-- name: CountActiveAPITokens :one
SELECT count(*) FROM api_tokens WHERE revoked_at IS NULL;

-- name: InsertAPIToken :one
INSERT INTO api_tokens (token_hash, label) VALUES ($1, $2) RETURNING *;

-- name: GetActiveAPITokenByHash :one
SELECT * FROM api_tokens WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: GetAPIToken :one
SELECT * FROM api_tokens WHERE id = $1;

-- name: ListAPITokens :many
SELECT * FROM api_tokens WHERE id > $1 ORDER BY id LIMIT $2;

-- name: RevokeAPIToken :exec
UPDATE api_tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL;

-- name: TouchAPIToken :exec
UPDATE api_tokens SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute');
