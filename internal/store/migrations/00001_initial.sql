-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
CREATE TABLE sources (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug            text UNIQUE NOT NULL,
    title           text NOT NULL,
    home_url        text NOT NULL,
    default_kind    text NOT NULL,
    image_url       text,
    extra           jsonb NOT NULL DEFAULT '{}',
    strategy        text NOT NULL,
    config          jsonb NOT NULL,
    cursor          jsonb,
    state           text NOT NULL,
    due_at          timestamptz,
    last_crawled_at timestamptz,
    last_error      text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id     bigint NOT NULL REFERENCES sources ON DELETE CASCADE,
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
    content_hash  bytea,
    discovered_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source_id, seq)
);

-- An entry's identity is its guid where it has one, because podcast items often
-- share a link or have none. Otherwise it is its URL.
CREATE UNIQUE INDEX entries_source_guid_key ON entries (source_id, guid) WHERE guid IS NOT NULL;
CREATE UNIQUE INDEX entries_source_url_key ON entries (source_id, url) WHERE guid IS NULL;

CREATE TABLE reads (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id  bigint NOT NULL REFERENCES sources,
    name       text NOT NULL,
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
    role         text NOT NULL,
    label        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_seen_at timestamptz
);

CREATE TABLE api_tokens (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    token_hash   bytea UNIQUE NOT NULL,
    label        text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);

CREATE TABLE crawl_log (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    source_id     bigint NOT NULL REFERENCES sources ON DELETE CASCADE,
    url           text NOT NULL,
    status        integer,
    etag          text,
    last_modified text,
    fetched_at    timestamptz NOT NULL DEFAULT now(),
    error         text
);

-- +goose Down
DROP TABLE crawl_log;
DROP TABLE api_tokens;
DROP TABLE feed_tokens;
DROP TABLE read_segments;
DROP TABLE reads;
DROP TABLE entries;
DROP TABLE sources;
