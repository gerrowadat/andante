-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: InsertSource :one
INSERT INTO sources (slug, title, home_url, default_kind, image_url, strategy, config, state)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1;

-- name: GetSourceForUpdate :one
SELECT * FROM sources WHERE id = $1 FOR UPDATE;

-- name: ListSources :many
SELECT * FROM sources WHERE id > $1 ORDER BY id LIMIT $2;

-- name: UpdateSource :one
UPDATE sources
SET title = $2, home_url = $3, default_kind = $4, image_url = $5, strategy = $6, config = $7, state = $8
WHERE id = $1
RETURNING *;

-- name: DeleteSource :execrows
DELETE FROM sources WHERE id = $1;

-- name: ListEntries :many
SELECT * FROM entries WHERE source_id = $1 AND seq > $2 ORDER BY seq LIMIT $3;
