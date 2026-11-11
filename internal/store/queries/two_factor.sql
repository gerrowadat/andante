-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- name: SetPendingTOTP :execrows
UPDATE users SET totp_sealed = $2, totp_last_step = 0 WHERE id = $1 AND totp_enabled_at IS NULL;

-- name: GetTOTP :one
SELECT totp_sealed, totp_enabled_at FROM users WHERE id = $1;

-- name: EnableTOTP :execrows
UPDATE users SET totp_enabled_at = sqlc.arg(at), totp_last_step = sqlc.arg(step)
WHERE id = sqlc.arg(id) AND totp_sealed IS NOT NULL AND totp_enabled_at IS NULL;

-- name: UseTOTPStep :execrows
UPDATE users SET totp_last_step = sqlc.arg(step)
WHERE id = sqlc.arg(id) AND totp_enabled_at IS NOT NULL AND totp_last_step < sqlc.arg(step);

-- name: ClearTOTP :exec
UPDATE users SET totp_sealed = NULL, totp_enabled_at = NULL, totp_last_step = 0 WHERE id = $1;

-- name: DeleteRecoveryCodes :exec
DELETE FROM recovery_codes WHERE user_id = $1;

-- name: InsertRecoveryCode :exec
INSERT INTO recovery_codes (user_id, code_hash) VALUES ($1, $2);

-- name: UseRecoveryCode :execrows
UPDATE recovery_codes SET used_at = sqlc.arg(at)
WHERE user_id = sqlc.arg(user_id) AND code_hash = sqlc.arg(code_hash) AND used_at IS NULL;

-- name: CountRecoveryCodes :one
SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: ListSealedTOTP :many
SELECT id, totp_sealed FROM users WHERE totp_sealed IS NOT NULL;

-- name: ResealTOTP :exec
UPDATE users SET totp_sealed = $2 WHERE id = $1;

-- name: ForgetTOTP :exec
UPDATE users SET totp_sealed = NULL, totp_enabled_at = NULL, totp_last_step = 0;

-- name: ForgetRecoveryCodes :exec
DELETE FROM recovery_codes;

-- name: GetRequireTOTP :one
SELECT coalesce((SELECT require_totp FROM settings), false)::boolean;

-- name: SetRequireTOTP :exec
INSERT INTO settings (require_totp) VALUES ($1)
ON CONFLICT (only_row) DO UPDATE SET require_totp = excluded.require_totp;

-- name: GetIdentityUser :one
SELECT u.* FROM identities i JOIN users u ON u.id = i.user_id
WHERE i.issuer = $1 AND i.subject = $2;

-- name: InsertIdentity :exec
INSERT INTO identities (user_id, issuer, subject, created_at) VALUES ($1, $2, $3, $4);
