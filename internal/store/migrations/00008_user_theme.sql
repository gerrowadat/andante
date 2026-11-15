-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The theme a user picked for the website, by name, or NULL to follow the site's.
-- The server checks the name, as for settings.theme.
ALTER TABLE users
    ADD COLUMN theme text CHECK (theme ~ '^[a-z][a-z0-9-]{0,31}$');

-- +goose Down
ALTER TABLE users DROP COLUMN theme;
