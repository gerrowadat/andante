// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func letUsersInvite(t *testing.T, s *store.Store, limit int32) {
	t.Helper()
	who := store.InvitesByUsers
	if err := s.SetInviteSettings(t.Context(), &who, &limit); err != nil {
		t.Fatal(err)
	}
}

func TestInviteSettings(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	got, err := s.GetInviteSettings(ctx)
	if err != nil || got != (store.InviteSettings{Who: store.InvitesByAdmins, Limit: 5}) {
		t.Errorf("by default = %+v, %v", got, err)
	}
	letUsersInvite(t, s, 2)
	limit := int32(9)
	if err := s.SetInviteSettings(ctx, nil, &limit); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetInviteSettings(ctx); err != nil || got != (store.InviteSettings{Who: store.InvitesByUsers, Limit: 9}) {
		t.Errorf("after = %+v, %v", got, err)
	}
	if on, err := s.RequireTOTP(ctx); err != nil || on {
		t.Errorf("two-factor moved: %v, %v", on, err)
	}
	bad := "everyone"
	if err := s.SetInviteSettings(ctx, &bad, nil); !errors.Is(err, store.ErrBadInvite) {
		t.Errorf("who = %q: %v", bad, err)
	}
}

func TestAUserCanOnlyInviteUsers(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	bob, _ := storetest.User(t, s, "bob", false)
	storetest.User(t, s, "carol", false)

	if _, _, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now); !errors.Is(err, store.ErrNotAllowed) {
		t.Errorf("while only admins can: %v", err)
	}
	letUsersInvite(t, s, 5)
	for what, req := range map[string]store.InviteRequest{
		"an admin": {Role: store.RoleAdmin},
		"a reset":  {Name: "carol", Role: store.RoleUser, Reset: true},
	} {
		if _, _, err := s.CreateInvite(ctx, bob, req, now); !errors.Is(err, store.ErrNotAllowed) {
			t.Errorf("%s: %v", what, err)
		}
	}
	invite, raw, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser, MaxUses: 2}, now)
	if err != nil || invite.CreatedBy != "" || invite.MaxUses != 2 {
		t.Fatalf("a user = %+v, %v", invite, err)
	}
	if got, err := s.LookupInvite(ctx, raw, now); err != nil || got.CreatedBy != "bob" || got.Name != "" {
		t.Errorf("looked up = %+v, %v", got, err)
	}

	// The invite stops working once bob couldn't make it any more, and comes back
	// with the setting.
	who := store.InvitesByAdmins
	if err := s.SetInviteSettings(ctx, &who, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupInvite(ctx, raw, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after only admins can: %v", err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "dan", "hash", client, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("redeemed after only admins can: %v", err)
	}
	letUsersInvite(t, s, 5)
	yes := true
	if _, err := s.UpdateUser(ctx, bob.UserID, store.UserChange{Disabled: &yes}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupInvite(ctx, raw, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after bob was disabled: %v", err)
	}
}

func TestAnInviteRecordsWhoMadeWho(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	bob, _ := storetest.User(t, s, "bob", false)
	letUsersInvite(t, s, 5)
	_, raw, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser, MaxUses: 2}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "Not A Name", "hash", client, now); !errors.Is(err, store.ErrBadName) {
		t.Errorf("a bad name: %v", err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "bob", "hash", client, now); !errors.Is(err, store.ErrConflict) {
		t.Errorf("a taken name: %v", err)
	}
	dan, _, err := s.RedeemInvite(ctx, raw, "dan", "hash", client, now)
	if err != nil || dan.Name != "dan" || dan.Role != store.RoleUser || dan.InvitedBy == nil || *dan.InvitedBy != "bob" {
		t.Fatalf("dan = %+v, %v", dan, err)
	}
	if got, err := s.LookupInvite(ctx, raw, now); err != nil || got.Uses != 1 {
		t.Errorf("after one use = %+v, %v", got, err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "eve", "hash", client, now); err != nil {
		t.Errorf("the second use: %v", err)
	}
	if _, err := s.LookupInvite(ctx, raw, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("used up = %v", err)
	}

	// The record outlives bob.
	if err := s.DeleteUser(ctx, store.Holder, bob.UserID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByName(ctx, "dan")
	if err != nil || got.InvitedBy == nil || *got.InvitedBy != "bob" {
		t.Errorf("dan after bob went = %+v, %v", got, err)
	}
}

func TestAnInviteWithANameIsForOne(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now().Truncate(time.Microsecond)
	for what, req := range map[string]store.InviteRequest{
		"a name for two":  {Name: "sam", Role: store.RoleUser, MaxUses: 2},
		"a reset nobody":  {Role: store.RoleUser, Reset: true},
		"none":            {Role: store.RoleUser, MaxUses: -1},
		"too many":        {Role: store.RoleUser, MaxUses: store.MaxInviteUses + 1},
		"too long":        {Role: store.RoleUser, Expires: now.Add(store.MaxInviteLife + time.Minute)},
		"already expired": {Role: store.RoleUser, Expires: now.Add(-time.Minute)},
	} {
		if _, _, err := s.CreateInvite(ctx, store.Holder, req, now); !errors.Is(err, store.ErrBadInvite) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if _, _, err := s.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "Sam", Role: store.RoleUser}, now); !errors.Is(err, store.ErrBadName) {
		t.Errorf("a bad name: %v", err)
	}
	invite, raw, err := s.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "sam", Role: store.RoleUser, Expires: now.Add(store.MaxInviteLife)}, now)
	if err != nil || invite.MaxUses != 1 || !invite.ExpiresAt.Equal(now.Add(store.MaxInviteLife)) {
		t.Fatalf("invite = %+v, %v", invite, err)
	}
	// The name on the invite wins over anything picked.
	sam, _, err := s.RedeemInvite(ctx, raw, "other", "hash", client, now)
	if err != nil || sam.Name != "sam" || sam.InvitedBy != nil {
		t.Errorf("sam = %+v, %v", sam, err)
	}
}

func TestALinkRefusesTheExtraPeopleArrivingAtOnce(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	_, raw, err := s.CreateInvite(ctx, store.Holder, store.InviteRequest{Role: store.RoleUser, MaxUses: 3}, now)
	if err != nil {
		t.Fatal(err)
	}
	const people = 10
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		made int
	)
	for i := range people {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.RedeemInvite(ctx, raw, fmt.Sprintf("p%d", i), "hash", client, now)
			switch {
			case err == nil:
				mu.Lock()
				made++
				mu.Unlock()
			case !errors.Is(err, store.ErrNotFound):
				t.Errorf("person %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	if made != 3 {
		t.Errorf("%d people got in on a link for 3", made)
	}
	users, err := s.ListUsers(ctx, 0, 100)
	if err != nil || len(users) != 3 {
		t.Errorf("users = %d, %v", len(users), err)
	}
}

func TestAUserCantGoOverTheLimit(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	bob, _ := storetest.User(t, s, "bob", false)
	letUsersInvite(t, s, 4)

	// Ten at once, for one place each: four get through.
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		made int
	)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now)
			switch {
			case err == nil:
				mu.Lock()
				made++
				mu.Unlock()
			case !errors.Is(err, store.ErrInviteLimit):
				t.Errorf("invite: %v", err)
			}
		}()
	}
	wg.Wait()
	if made != 4 {
		t.Fatalf("%d invites under a limit of 4", made)
	}

	// A used place is no longer outstanding, and nor is an expired one.
	invites, err := s.ListInvites(ctx, bob, 0, 10, now)
	if err != nil || len(invites) != 4 {
		t.Fatalf("bob's invites = %d, %v", len(invites), err)
	}
	if err := s.DeleteInvite(ctx, bob, invites[0].ID); err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser, Expires: now.Add(time.Hour)}, now)
	if err != nil {
		t.Fatalf("after withdrawing one: %v", err)
	}
	if _, _, err := s.RedeemInvite(ctx, raw, "dan", "hash", client, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now); err != nil {
		t.Errorf("after one was used: %v", err)
	}
	if _, _, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now); !errors.Is(err, store.ErrInviteLimit) {
		t.Errorf("over again: %v", err)
	}
	if _, _, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now.Add(InviteWeek)); err != nil {
		t.Errorf("once the rest expired: %v", err)
	}
}

// InviteWeek is past every invite's default life.
const InviteWeek = store.InviteLife + time.Minute

func TestWithdrawingInvites(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	bob, _ := storetest.User(t, s, "bob", false)
	carol, _ := storetest.User(t, s, "carol", false)
	letUsersInvite(t, s, 5)
	mine, raw, err := s.CreateInvite(ctx, bob, store.InviteRequest{Role: store.RoleUser}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateInvite(ctx, store.Holder, store.InviteRequest{Role: store.RoleAdmin}, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ListInvites(ctx, carol, 0, 10, now); err != nil || len(got) != 0 {
		t.Errorf("carol sees = %+v, %v", got, err)
	}
	if got, err := s.ListInvites(ctx, store.Holder, 0, 10, now); err != nil || len(got) != 2 {
		t.Errorf("an admin sees = %+v, %v", got, err)
	}
	if err := s.DeleteInvite(ctx, carol, mine.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("carol withdrawing bob's: %v", err)
	}
	if err := s.DeleteInvite(ctx, bob, mine.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LookupInvite(ctx, raw, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("withdrawn = %v", err)
	}
}
