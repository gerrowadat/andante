-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The token the server made to set up the first admin, when --bootstrap-token isn't
-- set. It's sealed with --secret so that every replica and every restart logs the same
-- one, and it's only here while there's no active admin.
CREATE TABLE bootstrap_token (
    only_row   boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    sealed     bytea NOT NULL,
    created_at timestamptz NOT NULL
);

-- An invite made with the bootstrap token. It stops working once there's an admin.
ALTER TABLE invites ADD COLUMN bootstrap boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE invites DROP COLUMN bootstrap;
DROP TABLE bootstrap_token;
