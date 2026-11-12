// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/seal"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

const wantMigrations = 5

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
	for _, table := range []string{"sources", "entries", "reads", "read_segments", "feed_tokens", "api_tokens", "recovery_codes", "identities", "settings", "bootstrap_token"} {
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

func TestUsersAndTheirTokens(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	user, token, err := s.CreateUser(ctx, "dave", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	actor, ok, err := s.AuthenticateAPIToken(ctx, token)
	if err != nil || !ok || actor.UserID != user.ID || !actor.Admin {
		t.Errorf("authenticate = %+v, %v, %v", actor, ok, err)
	}
	if _, _, err := s.CreateUser(ctx, "dave", store.RoleUser); !errors.Is(err, store.ErrConflict) {
		t.Errorf("a second dave = %v", err)
	}
	if _, ok, err := s.AuthenticateAPIToken(ctx, "not-a-token"); err != nil || ok {
		t.Errorf("a bad token = %v, %v", ok, err)
	}

	fresh, err := s.ResetUser(ctx, "dave")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.AuthenticateAPIToken(ctx, token); ok {
		t.Error("the old token survived a reset")
	}
	if _, ok, _ := s.AuthenticateAPIToken(ctx, fresh); !ok {
		t.Error("the new token doesn't work")
	}
	if _, err := s.ResetUser(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("resetting nobody = %v", err)
	}
}

func box(t *testing.T, secret string) *seal.Box {
	t.Helper()
	b, err := seal.New(secret)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func feedToken(t *testing.T, s *store.Store) string {
	t.Helper()
	ctx := t.Context()
	source, err := storetest.AddSource(t, s, db.UpsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: "https://a.example/", DefaultKind: "page", Strategy: "feed", Config: []byte("{}"), State: "tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := storetest.User(t, s, "reader", false)
	read, err := s.CreateRead(ctx, db.InsertReadParams{
		UserID: actor.UserID, SourceID: source.ID, Name: "r", CatchUp: "passthrough", Config: []byte("{}"),
	}, db.InsertSegmentParams{FromSeq: 0, StartsAt: time.Now(), Pacing: "paused", Timezone: "UTC", PerRelease: 1, Reason: "created", CreatedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.CreateFeedToken(ctx, actor, read.ID, "viewer", "me")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFeedTokensCanBeShownAgain(t *testing.T) {
	s := storetest.New(t)
	raw := feedToken(t, s)
	token, err := s.LookupFeedToken(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(token.TokenSealed), raw) {
		t.Error("the token is stored in the clear")
	}
	shown, err := s.RawFeedToken(token)
	if err != nil || shown != raw {
		t.Errorf("RawFeedToken = %q, %v", shown, err)
	}
}

func TestTheWrongSecretFailsAtTheStart(t *testing.T) {
	s := storetest.New(t)
	err := s.UseBox(t.Context(), box(t, strings.Repeat("x", 32)))
	if !errors.Is(err, store.ErrWrongSecret) {
		t.Errorf("UseBox = %v", err)
	}
}

func TestRotatingTheSecret(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	raw := feedToken(t, s)
	before := box(t, storetest.Secret)
	after := box(t, strings.Repeat("n", 32))

	if err := s.RotateSecret(ctx, after, before); !errors.Is(err, store.ErrWrongSecret) {
		t.Errorf("rotating from the wrong secret = %v", err)
	}
	if err := s.RotateSecret(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	if err := s.UseBox(ctx, before); !errors.Is(err, store.ErrWrongSecret) {
		t.Errorf("the old secret after rotating = %v", err)
	}
	if err := s.UseBox(ctx, after); err != nil {
		t.Fatal(err)
	}
	token, err := s.LookupFeedToken(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	if shown, err := s.RawFeedToken(token); err != nil || shown != raw {
		t.Errorf("after rotating: %q, %v", shown, err)
	}
}

func TestForgettingALostSecret(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	raw := feedToken(t, s)
	fresh := box(t, strings.Repeat("n", 32))
	if err := s.ForgetSecrets(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := s.UseBox(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	token, err := s.LookupFeedToken(ctx, raw)
	if err != nil {
		t.Fatalf("the token stopped working: %v", err)
	}
	if shown, err := s.RawFeedToken(token); err != nil || shown != "" {
		t.Errorf("RawFeedToken = %q, %v, want nothing", shown, err)
	}
}
