// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sync"
	"testing"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/jackc/pgx/v5/pgconn"
)

const wantMigrations = 2

func appliedMigrations(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	// goose records its own table as version 0.
	err := s.Pool().QueryRow(t.Context(), "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := storetest.New(t)
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if got := appliedMigrations(t, s); got != wantMigrations {
		t.Errorf("applied migrations = %d, want %d", got, wantMigrations)
	}
}

func TestConcurrentMigrationsApplyOnce(t *testing.T) {
	dsn := storetest.DSN(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() {
			s, err := store.Open(t.Context(), dsn)
			if err != nil {
				errs[i] = err
				return
			}
			defer s.Close()
			errs[i] = s.Migrate(t.Context())
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}

	s, err := store.Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := appliedMigrations(t, s); got != wantMigrations {
		t.Errorf("applied migrations = %d, want %d", got, wantMigrations)
	}
}

func TestSchemaHasEveryTable(t *testing.T) {
	s := storetest.New(t)
	for _, table := range []string{"sources", "entries", "reads", "read_segments", "feed_tokens", "api_tokens", "crawl_log"} {
		var exists bool
		err := s.Pool().QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", table).Scan(&exists)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Errorf("table %s does not exist", table)
		}
	}
}

func TestEntryIdentity(t *testing.T) {
	const insert = "INSERT INTO entries (source_id, seq, kind, url, guid) VALUES ($1, $2, 'page', $3, $4)"
	s := storetest.New(t)
	ctx := t.Context()
	var source int64
	err := s.Pool().QueryRow(ctx, `INSERT INTO sources (slug, title, home_url, default_kind, strategy, config, state)
		VALUES ('s', 't', 'https://example.com/', 'page', 'crawl', '{}', 'indexing') RETURNING id`).Scan(&source)
	if err != nil {
		t.Fatal(err)
	}

	url, guidA, guidB := "https://example.com/show", "a", "b"
	steps := []struct {
		name      string
		seq       int
		url, guid *string
		wantDup   bool
	}{
		{name: "first with a guid", seq: 1, url: &url, guid: &guidA},
		{name: "same link, different guid", seq: 2, url: &url, guid: &guidB},
		{name: "same guid", seq: 3, guid: &guidA, wantDup: true},
		{name: "no link, new guid", seq: 4, guid: new("c")},
		{name: "first without a guid", seq: 5, url: new("https://example.com/1")},
		{name: "same URL without a guid", seq: 6, url: new("https://example.com/1"), wantDup: true},
		{name: "link shared with a guid entry", seq: 7, url: &url},
	}
	for _, step := range steps {
		t.Run(step.name, func(t *testing.T) {
			_, err := s.Pool().Exec(ctx, insert, source, step.seq, step.url, step.guid)
			var pgErr *pgconn.PgError
			gotDup := errors.As(err, &pgErr) && pgErr.Code == "23505"
			if gotDup != step.wantDup || (err != nil && !gotDup) {
				t.Errorf("insert: %v, want duplicate %v", err, step.wantDup)
			}
		})
	}
}

func TestReadSegmentCreatedAtHasNoDefault(t *testing.T) {
	s := storetest.New(t)
	var hasDefault bool
	err := s.Pool().QueryRow(t.Context(), `SELECT column_default IS NOT NULL FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'read_segments' AND column_name = 'created_at'`).Scan(&hasDefault)
	if err != nil {
		t.Fatal(err)
	}
	if hasDefault {
		t.Error("read_segments.created_at has a default; it must come from the caller's now")
	}
}

func TestReady(t *testing.T) {
	t.Run("migrated", func(t *testing.T) {
		if err := storetest.New(t).Ready(t.Context()); err != nil {
			t.Errorf("Ready: %v", err)
		}
	})
	t.Run("migrations pending", func(t *testing.T) {
		if err := storetest.Unmigrated(t).Ready(t.Context()); err == nil {
			t.Error("Ready = nil, want an error")
		}
	})
	t.Run("database down", func(t *testing.T) {
		s, err := store.Open(t.Context(), "postgres://andante@127.0.0.1:1/andante?connect_timeout=1")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.Ready(t.Context()); err == nil {
			t.Error("Ready = nil, want an error")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		s := storetest.New(t)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := s.Ready(ctx); err == nil {
			t.Error("Ready = nil, want an error")
		}
	})
}

func storedHashes(t *testing.T, s *store.Store) [][]byte {
	t.Helper()
	rows, err := s.Pool().Query(t.Context(), "SELECT token_hash FROM api_tokens")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var hashes [][]byte
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	return hashes
}

func TestBootstrapGeneratesAToken(t *testing.T) {
	s := storetest.New(t)
	token, err := s.BootstrapAPIToken(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token %q is not 32 base64url bytes: %v", token, err)
	}
	want := sha256.Sum256([]byte(token))
	hashes := storedHashes(t, s)
	if len(hashes) != 1 || string(hashes[0]) != string(want[:]) {
		t.Errorf("stored hashes = %x, want only %x", hashes, want)
	}
}

func TestBootstrapStoresTheGivenToken(t *testing.T) {
	s := storetest.New(t)
	generated, err := s.BootstrapAPIToken(t.Context(), "given")
	if err != nil {
		t.Fatal(err)
	}
	if generated != "" {
		t.Errorf("generated = %q, want none", generated)
	}
	want := sha256.Sum256([]byte("given"))
	hashes := storedHashes(t, s)
	if len(hashes) != 1 || string(hashes[0]) != string(want[:]) {
		t.Errorf("stored hashes = %x, want only %x", hashes, want)
	}
}

func TestBootstrapDoesNothingOnceATokenExists(t *testing.T) {
	s := storetest.New(t)
	if _, err := s.BootstrapAPIToken(t.Context(), "first"); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "second"} {
		generated, err := s.BootstrapAPIToken(t.Context(), token)
		if err != nil {
			t.Fatal(err)
		}
		if generated != "" {
			t.Errorf("generated = %q, want none", generated)
		}
	}
	if got := len(storedHashes(t, s)); got != 1 {
		t.Errorf("stored tokens = %d, want 1", got)
	}
}

func TestConcurrentBootstrapCreatesOneToken(t *testing.T) {
	s := storetest.New(t)
	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		generated int
	)
	for range 8 {
		wg.Go(func() {
			token, err := s.BootstrapAPIToken(t.Context(), "")
			if err != nil {
				t.Error(err)
			}
			if token != "" {
				mu.Lock()
				generated++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if generated != 1 {
		t.Errorf("generated %d tokens, want 1", generated)
	}
	if got := len(storedHashes(t, s)); got != 1 {
		t.Errorf("stored tokens = %d, want 1", got)
	}
}
