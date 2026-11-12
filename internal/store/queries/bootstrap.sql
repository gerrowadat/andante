-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: GetBootstrapToken :one
SELECT sealed, created_at FROM bootstrap_token;

-- name: SetBootstrapToken :exec
INSERT INTO bootstrap_token (sealed, created_at) VALUES ($1, $2)
ON CONFLICT (only_row) DO UPDATE SET sealed = excluded.sealed, created_at = excluded.created_at;

-- name: DeleteBootstrapToken :exec
DELETE FROM bootstrap_token;

-- name: InsertBootstrapInvite :one
INSERT INTO invites (token_hash, name, role, created_at, expires_at, bootstrap)
VALUES ($1, $2, 'admin', $3, $4, true)
RETURNING *;

-- name: DeleteUnusedBootstrapInvites :exec
DELETE FROM invites WHERE bootstrap AND NOT EXISTS (SELECT 1 FROM invite_uses WHERE invite_id = invites.id);
