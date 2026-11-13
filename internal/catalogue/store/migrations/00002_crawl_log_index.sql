-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- For reading a source's crawl log newest first, and for finding a page's last
-- validators.
CREATE INDEX crawl_log_source ON crawl_log (source_id, id);

-- +goose Down
DROP INDEX crawl_log_source;
