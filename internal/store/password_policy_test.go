// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"testing"

	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestPasswordPolicy(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != password.DefaultPolicy {
		t.Errorf("by default = %+v, %v", got, err)
	}
	if err := s.SetPasswordPolicy(ctx, store.PolicyChange{MinLength: ptr(int32(20))}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPasswordPolicy(ctx, store.PolicyChange{NonLetters: ptr(int32(2)), Classes: ptr(int32(3))}); err != nil {
		t.Fatal(err)
	}
	want := password.Policy{MinLength: 20, NonLetters: 2, Classes: 3}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != want {
		t.Errorf("after = %+v, %v", got, err)
	}
	// Setting the policy leaves the other settings alone, and the other way round.
	if on, err := s.RequireTOTP(ctx); err != nil || on {
		t.Errorf("two-factor = %v, %v", on, err)
	}
	if err := s.SetRequireTOTP(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != want {
		t.Errorf("after two-factor = %+v, %v", got, err)
	}
	for _, bad := range []store.PolicyChange{
		{MinLength: ptr(int32(7))}, {MinLength: ptr(int32(129))}, {Classes: ptr(int32(5))}, {Classes: ptr(int32(-1))},
		{NonLetters: ptr(int32(21))}, {NonLetters: ptr(int32(-1))},
		// Checked against the length that's already there.
		{MinLength: ptr(int32(8)), NonLetters: ptr(int32(9))}, {NonLetters: ptr(int32(30))},
	} {
		if err := s.SetPasswordPolicy(ctx, bad); !errors.Is(err, password.ErrBadPolicy) {
			t.Errorf("%+v: %v, want ErrBadPolicy", bad, err)
		}
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != want {
		t.Errorf("after refusals = %+v, %v", got, err)
	}
}

func TestPasswordPolicyOnItsOwn(t *testing.T) {
	// The first write to settings can be this one.
	s := storetest.New(t)
	ctx := t.Context()
	if err := s.SetPasswordPolicy(ctx, store.PolicyChange{Classes: ptr(int32(2))}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != (password.Policy{MinLength: 8, NonLetters: 1, Classes: 2}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func ptr[T any](v T) *T { return &v }
