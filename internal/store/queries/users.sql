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
