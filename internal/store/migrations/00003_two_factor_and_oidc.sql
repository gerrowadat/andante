-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- The TOTP secret is sealed with --secret, because checking a code needs the secret
-- itself. It's set up but not on while totp_enabled_at is null. totp_last_step is the
-- last 30-second step a code was taken for, so that no code works twice.
ALTER TABLE users
    ADD COLUMN totp_sealed     bytea,
    ADD COLUMN totp_enabled_at timestamptz,
    ADD COLUMN totp_last_step  bigint NOT NULL DEFAULT 0;

-- Stored as hashes. They're random, so a fast hash is enough.
CREATE TABLE recovery_codes (
    id        bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id   bigint NOT NULL REFERENCES users ON DELETE CASCADE,
    code_hash bytea NOT NULL,
    used_at   timestamptz,
    UNIQUE (user_id, code_hash)
);

-- A login at an OpenID provider. It's matched on issuer and subject and never on email,
-- because an email claim is whatever the provider says it is.
CREATE TABLE identities (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users ON DELETE CASCADE,
    issuer     text NOT NULL,
    subject    text NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (issuer, subject)
);
CREATE INDEX identities_user ON identities (user_id);

-- How a session logged in: password or oidc. Requiring two-factor only applies to
-- the first, because a provider does its own.
ALTER TABLE sessions ADD COLUMN method text NOT NULL DEFAULT 'password';

CREATE TABLE settings (
    only_row     boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    require_totp boolean NOT NULL DEFAULT false
);

-- +goose Down
DROP TABLE settings;
ALTER TABLE sessions DROP COLUMN method;
DROP TABLE identities;
DROP TABLE recovery_codes;
ALTER TABLE users DROP COLUMN totp_last_step, DROP COLUMN totp_enabled_at, DROP COLUMN totp_sealed;
