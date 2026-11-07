-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- Null means this user can't log in with a password yet.
ALTER TABLE users ADD COLUMN password_hash text;

-- A session is looked up by the hash of its cookie, so that a database leak can't be
-- replayed. Expiry is worked out from the two times, and no job needs to clean up.
CREATE TABLE sessions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users ON DELETE CASCADE,
    token_hash   bytea UNIQUE NOT NULL,
    ip           text NOT NULL,
    user_agent   text NOT NULL,
    created_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    revoked_at   timestamptz
);
CREATE INDEX sessions_user ON sessions (user_id);

-- An invite with a user_id resets that user's password. One without creates the user.
CREATE TABLE invites (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash bytea UNIQUE NOT NULL,
    name       text NOT NULL,
    role       text NOT NULL,
    user_id    bigint REFERENCES users ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

-- Append-only. who is a name and not a key, so that it outlives the user.
CREATE TABLE audit_log (
    id     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    at     timestamptz NOT NULL,
    who    text NOT NULL,
    action text NOT NULL,
    target text NOT NULL,
    ip     text NOT NULL
);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE invites;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN password_hash;
