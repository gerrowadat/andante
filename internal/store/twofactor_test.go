// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

var client = store.Client{IP: "203.0.113.1", UserAgent: "test"}

func TestTwoFactor(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	user, _, err := s.CreateUser(ctx, "dave", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, user.ID); err != nil || got != (store.TOTP{}) {
		t.Errorf("before = %+v, %v", got, err)
	}
	if err := s.EnableTOTP(ctx, user.ID, 1, nil, now); !errors.Is(err, store.ErrConflict) {
		t.Errorf("turning on nothing = %v", err)
	}
	if err := s.StartTOTP(ctx, user.ID, "first"); err != nil {
		t.Fatal(err)
	}
	if err := s.StartTOTP(ctx, user.ID, "second"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, user.ID); err != nil || got != (store.TOTP{Secret: "second"}) {
		t.Errorf("pending = %+v, %v", got, err)
	}
	var sealed []byte
	if err := s.Pool().QueryRow(ctx, "SELECT totp_sealed FROM users WHERE id = $1", user.ID).Scan(&sealed); err != nil || strings.Contains(string(sealed), "second") {
		t.Errorf("the secret is stored in the clear: %v", err)
	}

	if err := s.EnableTOTP(ctx, user.ID, 100, []string{"aaaa-bbbb", "cccc-dddd"}, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, user.ID); err != nil || got != (store.TOTP{Secret: "second", Enabled: true}) {
		t.Errorf("on = %+v, %v", got, err)
	}
	if err := s.StartTOTP(ctx, user.ID, "third"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("starting again while on = %v", err)
	}

	// The step the code that turned it on was for is used up.
	for _, c := range []struct {
		step int64
		want bool
	}{{100, false}, {99, false}, {101, true}, {101, false}, {103, true}, {102, false}} {
		if ok, err := s.UseTOTPStep(ctx, user.ID, c.step); err != nil || ok != c.want {
			t.Errorf("step %d = %v, %v", c.step, ok, err)
		}
	}

	if ok, err := s.UseRecoveryCode(ctx, user.ID, "AAAA BBBB", now); err != nil || !ok {
		t.Errorf("a recovery code = %v, %v", ok, err)
	}
	if ok, err := s.UseRecoveryCode(ctx, user.ID, "aaaa-bbbb", now); err != nil || ok {
		t.Errorf("the same one again = %v, %v", ok, err)
	}
	if ok, err := s.UseRecoveryCode(ctx, user.ID, "eeee-ffff", now); err != nil || ok {
		t.Errorf("someone else's = %v, %v", ok, err)
	}
	if n, err := s.RecoveryCodesLeft(ctx, user.ID); err != nil || n != 1 {
		t.Errorf("left = %d, %v", n, err)
	}

	if err := s.DisableTOTP(ctx, user.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, user.ID); err != nil || got != (store.TOTP{}) {
		t.Errorf("off = %+v, %v", got, err)
	}
	if n, err := s.RecoveryCodesLeft(ctx, user.ID); err != nil || n != 0 {
		t.Errorf("left after = %d, %v", n, err)
	}
	if ok, err := s.UseTOTPStep(ctx, user.ID, 200); err != nil || ok {
		t.Errorf("a code with it off = %v, %v", ok, err)
	}
}

func enrolled(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	user, _, err := s.CreateUser(t.Context(), name, store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.StartTOTP(t.Context(), user.ID, "secret-of-"+name); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableTOTP(t.Context(), user.ID, 1, []string{"code"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return user.ID
}

func TestResettingAUserTurnsOffTwoFactor(t *testing.T) {
	s := storetest.New(t)
	id := enrolled(t, s, "dave")
	if _, err := s.ResetUser(t.Context(), "dave"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(t.Context(), id); err != nil || got.Enabled {
		t.Errorf("after a reset = %+v, %v", got, err)
	}
}

func TestTwoFactorAndTheSecret(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	id := enrolled(t, s, "dave")
	before, after := box(t, storetest.Secret), box(t, strings.Repeat("n", 32))
	if err := s.RotateSecret(ctx, before, after); err != nil {
		t.Fatal(err)
	}
	if err := s.UseBox(ctx, after); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, id); err != nil || got.Secret != "secret-of-dave" || !got.Enabled {
		t.Errorf("after rotating = %+v, %v", got, err)
	}

	fresh := box(t, strings.Repeat("f", 32))
	if err := s.ForgetSecrets(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if err := s.UseBox(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTOTP(ctx, id); err != nil || got != (store.TOTP{}) {
		t.Errorf("after forgetting = %+v, %v", got, err)
	}
}

func TestRequiringTwoFactor(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	if on, err := s.RequireTOTP(ctx); err != nil || on {
		t.Errorf("by default = %v, %v", on, err)
	}
	plain, _, err := s.CreateUser(ctx, "plain", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	withTOTP := enrolled(t, s, "careful")
	sso, sso1, err := s.CreateIdentityUser(ctx, "sso", store.Identity{Issuer: "https://idp", Subject: "1"}, client, now)
	if err != nil {
		t.Fatal(err)
	}
	session := func(id int64) string {
		raw, err := s.CreateSession(ctx, id, client, now)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	sessions := map[string]string{"plain": session(plain.ID), "careful": session(withTOTP), "sso": sso1}
	needs := func() map[string]bool {
		out := map[string]bool{}
		for name, raw := range sessions {
			got, err := s.LookupSession(ctx, raw, now)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = got.NeedsTOTP
		}
		return out
	}
	if got := needs(); got["plain"] || got["careful"] || got["sso"] {
		t.Errorf("off: %v", got)
	}
	if err := s.SetRequireTOTP(ctx, true); err != nil {
		t.Fatal(err)
	}
	if on, err := s.RequireTOTP(ctx); err != nil || !on {
		t.Errorf("on = %v, %v", on, err)
	}
	if got := needs(); !got["plain"] || got["careful"] || got["sso"] {
		t.Errorf("on: %v", got)
	}
	if got, err := s.LookupSession(ctx, sso1, now); err != nil || got.Method != store.MethodOIDC || got.HasPassword || got.UserID != sso.ID {
		t.Errorf("the provider's session = %+v, %v", got, err)
	}
}

func TestIdentities(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	dave, _, err := s.CreateUser(ctx, "dave", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	id := store.Identity{Issuer: "https://idp", Subject: "abc"}
	if _, _, err := s.LoginByIdentity(ctx, id, client, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("nobody's = %v", err)
	}

	// A new identity that would like to be called dave gets a name of its own.
	user, raw, err := s.CreateIdentityUser(ctx, "dave", id, client, now)
	if err != nil || user.Name != "dave-2" || user.ID == dave.ID || user.Role != store.RoleUser || raw == "" {
		t.Fatalf("create = %+v, %v", user, err)
	}
	if _, _, err := s.CreateIdentityUser(ctx, "someone", id, client, now); !errors.Is(err, store.ErrConflict) {
		t.Errorf("the same identity twice = %v", err)
	}
	if _, _, err := s.CreateIdentityUser(ctx, "Not OK", store.Identity{Issuer: "https://idp", Subject: "x"}, client, now); !errors.Is(err, store.ErrBadName) {
		t.Errorf("a bad name = %v", err)
	}
	long := strings.Repeat("a", 64)
	if _, _, err := s.CreateIdentityUser(ctx, long, store.Identity{Issuer: "https://idp", Subject: "l1"}, client, now); err != nil {
		t.Fatal(err)
	}
	if again, _, err := s.CreateIdentityUser(ctx, long, store.Identity{Issuer: "https://idp", Subject: "l2"}, client, now); err != nil || again.Name != strings.Repeat("a", 62)+"-2" {
		t.Errorf("a long name taken = %q, %v", again.Name, err)
	}

	got, _, err := s.LoginByIdentity(ctx, id, client, now)
	if err != nil || got.ID != user.ID {
		t.Errorf("login = %+v, %v", got, err)
	}
	// The same subject at another issuer is someone else.
	if _, _, err := s.LoginByIdentity(ctx, store.Identity{Issuer: "https://other", Subject: "abc"}, client, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another issuer = %v", err)
	}
	yes := true
	if _, err := s.UpdateUser(ctx, user.ID, store.UserChange{Disabled: &yes}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.LoginByIdentity(ctx, id, client, now); !errors.Is(err, store.ErrDisabled) {
		t.Errorf("disabled = %v", err)
	}
}

func TestRedeemingAnInviteWithAnIdentity(t *testing.T) {
	s := storetest.New(t)
	ctx, now := t.Context(), time.Now()
	dave, _, err := s.CreateUser(ctx, "dave", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	_, raw, err := s.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "dave", Reset: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	id := store.Identity{Issuer: "https://idp", Subject: "dave"}
	user, session, err := s.RedeemInviteWithIdentity(ctx, raw, "", id, client, now)
	if err != nil || user.ID != dave.ID || session == "" {
		t.Fatalf("redeem = %+v, %v", user, err)
	}
	if got, _, err := s.LoginByIdentity(ctx, id, client, now); err != nil || got.ID != dave.ID {
		t.Errorf("login = %+v, %v", got, err)
	}
	if _, _, err := s.RedeemInviteWithIdentity(ctx, raw, "", id, client, now); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("used twice = %v", err)
	}

	_, raw, err = s.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "alex", Role: store.RoleUser}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RedeemInviteWithIdentity(ctx, raw, "", id, client, now); !errors.Is(err, store.ErrConflict) {
		t.Errorf("someone else's identity = %v", err)
	}
	if _, err := s.LookupInvite(ctx, raw, now); err != nil {
		t.Errorf("the invite was used up by a failure: %v", err)
	}
}
