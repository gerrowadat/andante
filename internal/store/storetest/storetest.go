// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package storetest gives tests an isolated, migrated database schema.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// New returns a migrated store in a schema of its own, dropped on cleanup.
func New(t *testing.T) *store.Store {
	t.Helper()
	s := Unmigrated(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

// Unmigrated returns a store in a schema of its own, with nothing migrated.
func Unmigrated(t *testing.T) *store.Store {
	t.Helper()
	return Open(t, DSN(t))
}

// Open returns a store on dsn, closed on cleanup.
func Open(t *testing.T, dsn string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

// DSN creates an empty schema, dropped on cleanup, and returns a DSN that uses
// it. If ANDANTE_TEST_DATABASE_URL is unset the test skips, unless
// ANDANTE_REQUIRE_DB=1, which CI sets so that a skip cannot pass for a pass.
func DSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("ANDANTE_TEST_DATABASE_URL")
	if base == "" {
		if os.Getenv("ANDANTE_REQUIRE_DB") == "1" {
			t.Fatal("ANDANTE_REQUIRE_DB=1 but ANDANTE_TEST_DATABASE_URL is unset")
		}
		t.Skip("ANDANTE_TEST_DATABASE_URL is unset")
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(suffix)

	exec(t, base, "CREATE SCHEMA "+schema)
	t.Cleanup(func() { exec(t, base, "DROP SCHEMA "+schema+" CASCADE") })

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("ANDANTE_TEST_DATABASE_URL must be a URL: %v", err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

func exec(t *testing.T, dsn, sql string) {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
}

// Catalogue returns a migrated catalogue store in a schema of its own.
func Catalogue(t *testing.T) *catalogue.Store {
	t.Helper()
	s, err := catalogue.Open(t.Context(), DSN(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s
}

// AddSource puts a source in the server's copy, as the sync would. It takes the
// next free ID unless params has one, and fills in what a test doesn't care about.
func AddSource(t *testing.T, s *store.Store, params db.UpsertSourceParams) (db.Source, error) {
	t.Helper()
	if params.ID == 0 {
		if err := s.Pool().QueryRow(t.Context(), "SELECT coalesce(max(id), 0) + 1 FROM sources").Scan(&params.ID); err != nil {
			return db.Source{}, err
		}
	}
	if params.Extra == nil {
		params.Extra = []byte("{}")
	}
	if params.CreatedAt.IsZero() {
		params.CreatedAt = time.Now()
	}
	revision, err := s.SyncRevision(t.Context())
	if err != nil {
		return db.Source{}, err
	}
	if err := s.ApplyChanges(t.Context(), []store.Change{{Revision: revision + 1, Source: &params}}); err != nil {
		return db.Source{}, err
	}
	return s.GetSource(t.Context(), params.ID)
}
