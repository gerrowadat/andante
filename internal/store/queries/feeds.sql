-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: InsertFeedToken :one
INSERT INTO feed_tokens (read_id, token_hash, token_sealed, role, label) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetActiveFeedTokenByHash :one
SELECT * FROM feed_tokens WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: ListFeedTokens :many
SELECT * FROM feed_tokens WHERE read_id = $1 AND id > $2 ORDER BY id LIMIT $3;

-- name: RevokeFeedToken :execrows
UPDATE feed_tokens t SET revoked_at = coalesce(t.revoked_at, now())
FROM reads r
WHERE t.id = sqlc.arg(id) AND r.id = t.read_id AND (sqlc.arg(anyone)::boolean OR r.user_id = sqlc.arg(user_id));

-- name: ListSealedFeedTokens :many
SELECT id, token_sealed FROM feed_tokens WHERE token_sealed IS NOT NULL;

-- name: ResealFeedToken :exec
UPDATE feed_tokens SET token_sealed = $2 WHERE id = $1;

-- name: ForgetFeedTokenSecrets :exec
UPDATE feed_tokens SET token_sealed = NULL;

-- name: TouchFeedToken :exec
UPDATE feed_tokens SET last_seen_at = now()
WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < now() - interval '1 hour');

-- name: ListEntriesInRange :many
SELECT * FROM entries WHERE source_id = $1 AND seq >= $2 AND seq <= $3 ORDER BY seq DESC;

-- name: LatestSegmentID :one
SELECT coalesce(max(id), 0)::bigint FROM read_segments WHERE read_id = $1;
