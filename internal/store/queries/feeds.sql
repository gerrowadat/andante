-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: InsertFeedToken :one
INSERT INTO feed_tokens (read_id, token_hash, role, label) VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetActiveFeedTokenByHash :one
SELECT * FROM feed_tokens WHERE token_hash = $1 AND revoked_at IS NULL;

-- name: ListFeedTokens :many
SELECT * FROM feed_tokens WHERE read_id = $1 AND id > $2 ORDER BY id LIMIT $3;

-- name: RevokeFeedToken :execrows
UPDATE feed_tokens SET revoked_at = coalesce(revoked_at, now()) WHERE id = $1;

-- name: TouchFeedToken :exec
UPDATE feed_tokens SET last_seen_at = now()
WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < now() - interval '1 hour');

-- name: ListEntriesInRange :many
SELECT * FROM entries WHERE source_id = $1 AND seq >= $2 AND seq <= $3 ORDER BY seq DESC;

-- name: LatestSegmentID :one
SELECT coalesce(max(id), 0)::bigint FROM read_segments WHERE read_id = $1;
