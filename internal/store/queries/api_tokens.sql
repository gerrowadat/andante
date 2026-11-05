-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: LockAPITokenBootstrap :exec
SELECT pg_advisory_xact_lock($1);

-- name: CountAPITokens :one
SELECT count(*) FROM api_tokens;

-- name: InsertAPIToken :exec
INSERT INTO api_tokens (token_hash, label) VALUES ($1, $2);
