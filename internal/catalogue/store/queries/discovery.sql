-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: ListDueSources :many
-- A source with no due_at is not forced: the crawl loop works out whether it
-- needs indexing. One with a due_at waits for it. A tail source with no due_at is
-- parked, because nobody reads it, and waits for demand.
SELECT * FROM sources
WHERE (state = 'indexing' AND due_at IS NULL) OR (state IN ('indexing', 'tail') AND due_at <= $1)
ORDER BY due_at NULLS LAST, id;

-- name: EntryStats :one
SELECT count(*)::bigint AS count, coalesce(max(seq), -1)::integer AS max_seq
FROM entries WHERE source_id = $1;

-- name: EntryKnown :one
SELECT EXISTS (
    SELECT 1 FROM entries
    WHERE source_id = sqlc.arg(source_id) AND (
        guid = sqlc.narg(guid)
        OR (sqlc.narg(guid)::text IS NULL AND (url = sqlc.narg(url) OR content_hash = sqlc.narg(content_hash)))
    )
);

-- name: InsertEntry :exec
INSERT INTO entries (
    source_id, seq, kind, url, guid, title, summary, content_html, images, alt_text,
    enclosures, duration, extra, original_date, content_hash
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15);

-- name: UpdateEntryContent :exec
UPDATE entries SET title = $3, images = $4, alt_text = $5
WHERE source_id = $1 AND url = $2;

-- name: RecordSourceOutcome :exec
-- A source paused during a crawl stays paused.
UPDATE sources
SET cursor = coalesce(sqlc.narg(cursor), cursor),
    state = CASE WHEN state = 'paused' THEN state ELSE sqlc.arg(state)::text END,
    due_at = sqlc.narg(due_at),
    last_crawled_at = sqlc.arg(last_crawled_at),
    last_error = sqlc.narg(last_error)
WHERE id = sqlc.arg(id);

-- name: UpdateSourceMetadata :exec
UPDATE sources SET image_url = coalesce(sqlc.narg(image_url), image_url), extra = sqlc.arg(extra)
WHERE id = sqlc.arg(id);

-- name: RequestCrawl :execrows
UPDATE sources SET due_at = now() WHERE id = $1;

-- name: InsertCrawlLog :exec
INSERT INTO crawl_log (source_id, url, status, etag, last_modified, error)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: LatestValidators :one
SELECT etag, last_modified FROM crawl_log
WHERE source_id = $1 AND url = $2 AND status = 200
ORDER BY id DESC LIMIT 1;

-- name: InsertCrawlEvent :one
INSERT INTO crawl_log (source_id, kind, level, message, url, body)
VALUES (sqlc.arg(source_id), 'event', sqlc.arg(level), sqlc.arg(message), sqlc.narg(url), sqlc.narg(body))
RETURNING id;

-- name: ClearCrawlPages :exec
-- Keeps a source's latest page and drops the rest.
UPDATE crawl_log SET body = NULL
WHERE source_id = sqlc.arg(source_id) AND body IS NOT NULL AND id <> sqlc.arg(keep);

-- name: ListCrawlLog :many
SELECT id, kind, url, status, error, level, message, fetched_at, (body IS NOT NULL)::boolean AS has_page
FROM crawl_log
WHERE source_id = sqlc.arg(source_id) AND id < sqlc.arg(before)
  AND (sqlc.narg(kind)::text IS NULL OR kind = sqlc.narg(kind))
ORDER BY id DESC LIMIT sqlc.arg(row_limit);

-- name: GetCrawlPage :one
SELECT url, fetched_at, body FROM crawl_log
WHERE source_id = sqlc.arg(source_id) AND id = sqlc.arg(id) AND body IS NOT NULL;
