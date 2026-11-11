-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: InsertUser :one
INSERT INTO users (name, role) VALUES ($1, $2) RETURNING *;

-- name: GetUser :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByName :one
SELECT * FROM users WHERE name = $1;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: SecretCheck :one
SELECT sealed FROM secret_check;

-- name: SetSecretCheck :exec
INSERT INTO secret_check (sealed) VALUES ($1)
ON CONFLICT (only_row) DO UPDATE SET sealed = excluded.sealed;

-- name: ListUsers :many
SELECT * FROM users WHERE id > $1 ORDER BY id LIMIT $2;

-- name: GetUserForUpdate :one
SELECT * FROM users WHERE id = $1 FOR UPDATE;

-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL;

-- name: UpdateUser :one
UPDATE users SET role = $2, disabled_at = $3 WHERE id = $1 RETURNING *;

-- name: DeleteReadsOfUser :exec
DELETE FROM reads WHERE user_id = $1;

-- name: DeleteAPITokensOfUser :exec
DELETE FROM api_tokens WHERE user_id = $1;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = $1;

-- name: LockUsers :exec
SELECT pg_advisory_xact_lock($1);
