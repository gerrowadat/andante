-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The website's theme, by name. The themes are built into the server, which checks
-- the name; this only keeps it to something that could be one.
ALTER TABLE settings
    ADD COLUMN theme text NOT NULL DEFAULT 'default' CHECK (theme ~ '^[a-z][a-z0-9-]{0,31}$');

-- +goose Down
ALTER TABLE settings DROP COLUMN theme;
