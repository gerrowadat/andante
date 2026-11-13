-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The crawl log also records what the crawl decided and what puzzled it, as
-- events. An event may carry the body of the page it gave up on, and only the
-- latest such page per source is kept.
ALTER TABLE crawl_log
    ALTER COLUMN url DROP NOT NULL,
    ADD COLUMN kind text NOT NULL DEFAULT 'fetch' CHECK (kind IN ('fetch', 'event')),
    ADD COLUMN level text CHECK (level IN ('debug', 'info', 'warn', 'error')),
    ADD COLUMN message text,
    ADD COLUMN body bytea,
    ADD CONSTRAINT crawl_log_shape CHECK (
        (kind = 'fetch' AND url IS NOT NULL) OR
        (kind = 'event' AND level IS NOT NULL AND message IS NOT NULL)
    );

-- +goose Down
DELETE FROM crawl_log WHERE kind = 'event';
ALTER TABLE crawl_log
    DROP CONSTRAINT crawl_log_shape,
    DROP COLUMN body,
    DROP COLUMN message,
    DROP COLUMN level,
    DROP COLUMN kind,
    ALTER COLUMN url SET NOT NULL;
