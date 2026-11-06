-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: GetSource :one
SELECT * FROM sources WHERE id = $1;

-- name: ListSources :many
SELECT * FROM sources WHERE id > $1 ORDER BY id LIMIT $2;

-- name: ListSourceIDs :many
SELECT id FROM sources ORDER BY id;

-- name: FindSourceByFeedURL :one
SELECT * FROM sources WHERE config->>'feed_url' = $1 ORDER BY id LIMIT 1;

-- name: ListEntries :many
SELECT * FROM entries WHERE source_id = $1 AND seq > $2 ORDER BY seq LIMIT $3;

-- name: SyncRevision :one
SELECT revision FROM sync_state FOR UPDATE;

-- name: SetSyncRevision :exec
UPDATE sync_state SET revision = $1;

-- name: PeekSyncRevision :one
SELECT revision FROM sync_state;

-- name: UpsertSource :exec
INSERT INTO sources (id, slug, title, home_url, default_kind, image_url, extra, strategy, config,
                     state, due_at, last_crawled_at, last_error, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
ON CONFLICT (id) DO UPDATE
SET slug = excluded.slug, title = excluded.title, home_url = excluded.home_url,
    default_kind = excluded.default_kind, image_url = excluded.image_url, extra = excluded.extra,
    strategy = excluded.strategy, config = excluded.config, state = excluded.state,
    due_at = excluded.due_at, last_crawled_at = excluded.last_crawled_at,
    last_error = excluded.last_error;

-- name: UpsertEntry :exec
INSERT INTO entries (id, source_id, seq, kind, url, guid, title, summary, content_html, images,
                     alt_text, enclosures, duration, extra, original_date, discovered_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (id) DO UPDATE
SET kind = excluded.kind, url = excluded.url, guid = excluded.guid, title = excluded.title,
    summary = excluded.summary, content_html = excluded.content_html, images = excluded.images,
    alt_text = excluded.alt_text, enclosures = excluded.enclosures, duration = excluded.duration,
    extra = excluded.extra, original_date = excluded.original_date;

-- name: CountReadsOfSource :one
SELECT count(*) FROM reads WHERE source_id = $1;

-- name: DeleteEntriesOfSource :exec
DELETE FROM entries WHERE source_id = $1;

-- name: DeleteSource :exec
DELETE FROM sources WHERE id = $1;
