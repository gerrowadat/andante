-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
-- sources and entries are a copy of the catalogue's, written only by the sync.
-- An entry can arrive before its source, because a source's revision moves on
-- every crawl and an entry's doesn't, so nothing ties one to the other.
CREATE TABLE sources (
    id              bigint PRIMARY KEY,
    slug            text UNIQUE NOT NULL,
    title           text NOT NULL,
    home_url        text NOT NULL,
    default_kind    text NOT NULL,
    image_url       text,
    extra           jsonb NOT NULL DEFAULT '{}',
    strategy        text NOT NULL,
    config          jsonb NOT NULL,
    state           text NOT NULL,
    due_at          timestamptz,
    last_crawled_at timestamptz,
    last_error      text,
    created_at      timestamptz NOT NULL
);

-- available_at is when this server first had the entry. It's set once, and it's
-- what a release waits for, so an entry can't count as released before it could
-- be shown.
CREATE TABLE entries (
    id            bigint PRIMARY KEY,
    source_id     bigint NOT NULL,
    seq           integer NOT NULL,
    kind          text NOT NULL,
    url           text,
    guid          text,
    title         text,
    summary       text,
    content_html  text,
    images        text[] NOT NULL DEFAULT '{}',
    alt_text      text,
    enclosures    jsonb NOT NULL DEFAULT '[]',
    duration      interval,
    extra         jsonb NOT NULL DEFAULT '{}',
    original_date timestamptz,
    discovered_at timestamptz NOT NULL,
    available_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, seq)
);

CREATE TABLE sync_state (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    revision bigint NOT NULL
);
INSERT INTO sync_state (revision) VALUES (0);

CREATE TABLE users (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        text UNIQUE NOT NULL,
    role        text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    disabled_at timestamptz
);

-- Sealed with --secret, so that starting with the wrong one fails at once and not
-- when someone asks for a feed URL.
CREATE TABLE secret_check (
    only_row boolean PRIMARY KEY DEFAULT true CHECK (only_row),
    sealed   bytea NOT NULL
);

CREATE TABLE reads (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users,
    source_id  bigint NOT NULL REFERENCES sources,
    name       text NOT NULL,
    start_seq  integer NOT NULL DEFAULT 0,
    catch_up   text NOT NULL DEFAULT 'passthrough',
    buffer     integer NOT NULL DEFAULT 0,
    config     jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only. created_at has no default: it is when the segment takes effect,
-- written from the same now as from_seq. starts_at can predate it.
CREATE TABLE read_segments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    read_id     bigint NOT NULL REFERENCES reads ON DELETE CASCADE,
    from_seq    integer NOT NULL,
    starts_at   timestamptz NOT NULL,
    pacing      text NOT NULL,
    rrule       text,
    timezone    text NOT NULL,
    per_release integer NOT NULL DEFAULT 1,
    speed       numeric,
    min_gap     interval,
    max_gap     interval,
    default_gap interval,
    reason      text NOT NULL,
    created_at  timestamptz NOT NULL
);

CREATE TABLE feed_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    read_id      bigint NOT NULL REFERENCES reads ON DELETE CASCADE,
    token_hash   bytea UNIQUE NOT NULL,
    -- The raw token, sealed with --secret, so that its URL can be shown again. Null
    -- if the secret was lost: the token still works, but nobody can show it.
    token_sealed bytea,
    role         text NOT NULL,
    label        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_seen_at timestamptz
);

CREATE TABLE api_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users,
    token_hash   bytea UNIQUE NOT NULL,
    label        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);

-- +goose Down
DROP TABLE api_tokens;
DROP TABLE feed_tokens;
DROP TABLE read_segments;
DROP TABLE reads;
DROP TABLE secret_check;
DROP TABLE users;
DROP TABLE sync_state;
DROP TABLE entries;
DROP TABLE sources;
