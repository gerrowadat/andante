-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The first seq a read covers. Nothing else records it: the first segment's
-- from_seq also counts the entries released at the start.
ALTER TABLE reads ADD COLUMN start_seq integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE reads DROP COLUMN start_seq;
