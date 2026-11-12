// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestTheBootstrapTokenLastsADay(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if _, err := s.StoredBootstrapToken(ctx, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("before one is made = %v", err)
	}
	first, until, err := s.BootstrapToken(ctx, now)
	if err != nil || first == "" || !until.Equal(now.Add(store.BootstrapLife)) {
		t.Fatalf("first = %q, %s, %v", first, until, err)
	}
	again, _, err := s.BootstrapToken(ctx, now.Add(time.Hour))
	if err != nil || again != first {
		t.Errorf("a restart made a new token: %q, %v", again, err)
	}
	if got, err := s.StoredBootstrapToken(ctx, now.Add(time.Hour)); err != nil || got != first {
		t.Errorf("stored = %q, %v", got, err)
	}

	later := now.Add(store.BootstrapLife)
	if _, err := s.StoredBootstrapToken(ctx, later); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a day later = %v", err)
	}
	fresh, _, err := s.BootstrapToken(ctx, later)
	if err != nil || fresh == first {
		t.Errorf("after it ran out = %q, %v", fresh, err)
	}

	if err := s.ForgetBootstrapToken(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StoredBootstrapToken(ctx, later); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after forgetting = %v", err)
	}
}

func TestAnAdminEndsTheBootstrapToken(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if _, _, err := s.BootstrapToken(ctx, now); err != nil {
		t.Fatal(err)
	}
	// Users who aren't admins don't count.
	if _, _, err := s.CreateUser(ctx, "alex", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if needs, err := s.NeedsAdmin(ctx); err != nil || !needs {
		t.Errorf("needs an admin = %v, %v", needs, err)
	}
	if _, _, err := s.CreateUser(ctx, "dave", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if needs, err := s.NeedsAdmin(ctx); err != nil || needs {
		t.Errorf("needs an admin = %v, %v", needs, err)
	}
	if _, _, err := s.BootstrapToken(ctx, now); !errors.Is(err, store.ErrHaveAdmin) {
		t.Errorf("with an admin = %v", err)
	}
	if _, err := s.StoredBootstrapToken(ctx, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the stored token outlived the admin check: %v", err)
	}
	if _, _, err := s.CreateBootstrapInvite(ctx, "sam", now); !errors.Is(err, store.ErrHaveAdmin) {
		t.Errorf("an invite with an admin = %v", err)
	}
	if _, _, err := s.CreateBootstrapAdmin(ctx, "sam"); !errors.Is(err, store.ErrHaveAdmin) {
		t.Errorf("an admin with an admin = %v", err)
	}
}

func TestABootstrapInvite(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if _, _, err := s.CreateUser(ctx, "alex", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateBootstrapInvite(ctx, "alex", now); !errors.Is(err, store.ErrConflict) {
		t.Errorf("taking over alex = %v", err)
	}
	if _, _, err := s.CreateBootstrapInvite(ctx, "Not A Name", now); !errors.Is(err, store.ErrBadName) {
		t.Errorf("a bad name = %v", err)
	}
	if _, _, err := s.BootstrapToken(ctx, now); err != nil {
		t.Fatal(err)
	}

	invite, old, err := s.CreateBootstrapInvite(ctx, "dave", now)
	if err != nil || invite.Role != store.RoleAdmin || !invite.Bootstrap {
		t.Fatalf("invite = %+v, %v", invite, err)
	}
	_, raw, err := s.CreateBootstrapInvite(ctx, "dave", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupInvite(ctx, old, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the earlier invite still works: %v", err)
	}
	user, _, err := s.RedeemInvite(ctx, raw, "", "hash", client, now)
	if err != nil || user.Role != store.RoleAdmin {
		t.Fatalf("redeem = %+v, %v", user, err)
	}
	if _, err := s.StoredBootstrapToken(ctx, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the token outlived the first admin: %v", err)
	}
}

func TestABootstrapInviteDiesWithAnotherAdmin(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	_, raw, err := s.CreateBootstrapInvite(ctx, "dave", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateBootstrapAdmin(ctx, "sam"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "", "hash", client, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("redeem after another admin = %v", err)
	}
	if _, err := s.GetUserByName(ctx, "dave"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("dave was made anyway: %v", err)
	}
}

func TestRacingBootstrapsMakeOneAdmin(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	const racers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		made int
	)
	for i := range racers {
		wg.Go(func() {
			_, _, err := s.CreateBootstrapAdmin(ctx, "admin"+strings.Repeat("x", i))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				made++
			case !errors.Is(err, store.ErrHaveAdmin):
				t.Errorf("racer %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	if made != 1 {
		t.Errorf("%d admins were made", made)
	}
}

func TestRotatingTheSecretForgetsTheBootstrapToken(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if _, _, err := s.BootstrapToken(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := s.RotateSecret(ctx, box(t, storetest.Secret), box(t, strings.Repeat("n", 32))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StoredBootstrapToken(ctx, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after rotating = %v", err)
	}
}
