// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"testing"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestTheme(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	if got, err := s.GetTheme(ctx); err != nil || got != "default" {
		t.Errorf("by default = %q, %v", got, err)
	}
	// The first write to settings can be this one.
	if err := s.SetTheme(ctx, "paper"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTheme(ctx); err != nil || got != "paper" {
		t.Errorf("after = %q, %v", got, err)
	}
	// It leaves the other settings alone, and they leave it alone.
	if err := s.SetRequireTOTP(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetTheme(ctx); err != nil || got != "paper" {
		t.Errorf("after two-factor = %q, %v", got, err)
	}
	if on, err := s.RequireTOTP(ctx); err != nil || !on {
		t.Errorf("two-factor = %v, %v", on, err)
	}
	if err := s.SetTheme(ctx, "Not; a name"); err == nil {
		t.Error("the database took a name that can't be a theme's")
	}
}

func TestUserTheme(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	bob, _ := storetest.User(t, s, "bob", false)
	ann, _ := storetest.User(t, s, "ann", false)
	user, err := s.SetUserTheme(ctx, bob, "slate")
	if err != nil || user.Theme == nil || *user.Theme != "slate" {
		t.Fatalf("set = %+v, %v", user.Theme, err)
	}
	if got, err := s.GetUser(ctx, ann.UserID); err != nil || got.Theme != nil {
		t.Errorf("ann's = %+v, %v", got.Theme, err)
	}
	if site, err := s.GetTheme(ctx); err != nil || site != "default" {
		t.Errorf("the site's = %q, %v", site, err)
	}
	if user, err := s.SetUserTheme(ctx, bob, ""); err != nil || user.Theme != nil {
		t.Errorf("cleared = %+v, %v", user.Theme, err)
	}
	if _, err := s.SetUserTheme(ctx, bob, "Not; a name"); err == nil {
		t.Error("the database took a name that can't be a theme's")
	}
	if _, err := s.SetUserTheme(ctx, store.Actor{UserID: 999999}, "slate"); err == nil {
		t.Error("set a theme for a user there isn't")
	}
}
