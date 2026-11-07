-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: InsertRead :one
INSERT INTO reads (user_id, source_id, name, catch_up, buffer, config, start_seq)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetRead :one
SELECT * FROM reads WHERE id = sqlc.arg(id) AND (sqlc.arg(anyone)::boolean OR user_id = sqlc.arg(user_id));

-- name: GetReadForUpdate :one
SELECT * FROM reads WHERE id = sqlc.arg(id) AND (sqlc.arg(anyone)::boolean OR user_id = sqlc.arg(user_id)) FOR UPDATE;

-- name: ListReads :many
SELECT * FROM reads WHERE id > sqlc.arg(after) AND (sqlc.arg(anyone)::boolean OR user_id = sqlc.arg(user_id)) ORDER BY id LIMIT sqlc.arg(page_size);

-- name: ListReadsOfSource :many
SELECT * FROM reads WHERE source_id = $1 ORDER BY id;

-- name: UpdateRead :one
UPDATE reads SET name = sqlc.arg(name), config = sqlc.arg(config)
WHERE id = sqlc.arg(id) AND (sqlc.arg(anyone)::boolean OR user_id = sqlc.arg(user_id))
RETURNING *;

-- name: DeleteRead :execrows
DELETE FROM reads WHERE id = sqlc.arg(id) AND (sqlc.arg(anyone)::boolean OR user_id = sqlc.arg(user_id));

-- name: InsertSegment :exec
INSERT INTO read_segments (
    read_id, from_seq, starts_at, pacing, rrule, timezone, per_release,
    speed, min_gap, max_gap, default_gap, reason, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13);

-- name: ListSegments :many
SELECT * FROM read_segments WHERE read_id = $1 ORDER BY id;

-- name: ListSegmentsPage :many
SELECT * FROM read_segments WHERE read_id = $1 AND id > $2 ORDER BY id LIMIT $3;

-- name: ListEntryTimes :many
SELECT seq, original_date, available_at AS discovered_at FROM entries WHERE source_id = $1 ORDER BY seq;
