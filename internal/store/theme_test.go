// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"testing"

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
