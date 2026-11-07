-- SPDX-License-Identifier: Apache-2.0
-- Copyright (C) 2026 Dave O'Connor

-- +goose Up
CREATE SEQUENCE catalogue_revision;

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
    created_at      timestamptz NOT NULL DEFAULT now(),
    canonical_key   text UNIQUE NOT NULL,
    home_key        text NOT NULL,
    demand_seq      integer,
    revision        bigint NOT NULL DEFAULT 0
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
    revision      bigint NOT NULL DEFAULT 0,
    UNIQUE (source_id, seq)
);

-- An entry's identity is its guid where it has one, because podcast items often
-- share a link or have none. Otherwise it is its URL.
CREATE UNIQUE INDEX entries_source_guid_key ON entries (source_id, guid) WHERE guid IS NOT NULL;
CREATE UNIQUE INDEX entries_source_url_key ON entries (source_id, url) WHERE guid IS NULL;
CREATE INDEX sources_home_key ON sources (home_key);
CREATE INDEX sources_revision ON sources (revision);
CREATE INDEX entries_revision ON entries (revision);

CREATE TABLE deleted_sources (
    id       bigint PRIMARY KEY,
    revision bigint NOT NULL
);
CREATE INDEX deleted_sources_revision ON deleted_sources (revision);

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

-- Every write takes this lock until it commits, so revisions commit in order and
-- a reader that has seen one has seen everything below it. 1668511849 spells
-- "cati" in ASCII.
-- +goose StatementBegin
CREATE FUNCTION next_revision() RETURNS bigint LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock(1668511849);
    RETURN nextval('catalogue_revision');
END
$$;

CREATE FUNCTION set_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    NEW.revision := next_revision();
    RETURN NEW;
END
$$;

CREATE FUNCTION record_deleted_source() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO deleted_sources (id, revision) VALUES (OLD.id, next_revision());
    RETURN OLD;
END
$$;
-- +goose StatementEnd

-- demand_seq and cursor are left out: the server has no copy of them, so changing
-- them is not a change it needs to hear about.
CREATE TRIGGER sources_revision
    BEFORE INSERT OR UPDATE OF title, home_url, default_kind, image_url, extra, strategy, config,
        state, due_at, last_crawled_at, last_error
    ON sources FOR EACH ROW EXECUTE FUNCTION set_revision();
CREATE TRIGGER entries_revision
    BEFORE INSERT OR UPDATE ON entries FOR EACH ROW EXECUTE FUNCTION set_revision();
CREATE TRIGGER sources_deleted
    AFTER DELETE ON sources FOR EACH ROW EXECUTE FUNCTION record_deleted_source();

-- +goose Down
DROP TABLE crawl_log;
DROP TABLE deleted_sources;
DROP TABLE entries;
DROP TABLE sources;
DROP FUNCTION record_deleted_source;
DROP FUNCTION set_revision;
DROP FUNCTION next_revision;
DROP SEQUENCE catalogue_revision;
