-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- What a new password has to be: at least password_min_length characters,
-- password_non_letters of them not letters, and password_classes of lower-case letters,
-- capitals, digits and anything else. Passwords already set aren't checked again.
ALTER TABLE settings
    ADD COLUMN password_min_length integer NOT NULL DEFAULT 8 CHECK (password_min_length BETWEEN 8 AND 128),
    ADD COLUMN password_non_letters integer NOT NULL DEFAULT 1 CHECK (password_non_letters BETWEEN 0 AND password_min_length),
    ADD COLUMN password_classes integer NOT NULL DEFAULT 0 CHECK (password_classes BETWEEN 0 AND 4);

-- +goose Down
ALTER TABLE settings DROP COLUMN password_classes, DROP COLUMN password_non_letters, DROP COLUMN password_min_length;
