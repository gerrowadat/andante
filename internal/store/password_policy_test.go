// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store_test

import (
	"errors"
	"testing"

	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/store/storetest"
)

func TestPasswordPolicy(t *testing.T) {
	s := storetest.New(t)
	ctx := t.Context()
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != password.DefaultPolicy {
		t.Errorf("by default = %+v, %v", got, err)
	}
	length, classes := int32(20), int32(3)
	if err := s.SetPasswordPolicy(ctx, &length, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPasswordPolicy(ctx, nil, &classes); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != (password.Policy{MinLength: 20, Classes: 3}) {
		t.Errorf("after = %+v, %v", got, err)
	}
	// Setting the policy leaves the other settings alone, and the other way round.
	if on, err := s.RequireTOTP(ctx); err != nil || on {
		t.Errorf("two-factor = %v, %v", on, err)
	}
	if err := s.SetRequireTOTP(ctx, true); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != (password.Policy{MinLength: 20, Classes: 3}) {
		t.Errorf("after two-factor = %+v, %v", got, err)
	}
	for _, bad := range []struct{ length, classes *int32 }{{ptr(int32(7)), nil}, {ptr(int32(129)), nil}, {nil, ptr(int32(5))}, {nil, ptr(int32(-1))}} {
		if err := s.SetPasswordPolicy(ctx, bad.length, bad.classes); !errors.Is(err, password.ErrBadPolicy) {
			t.Errorf("%v, %v: %v, want ErrBadPolicy", bad.length, bad.classes, err)
		}
	}
}

func TestPasswordPolicyOnItsOwn(t *testing.T) {
	// The first write to settings can be this one.
	s := storetest.New(t)
	ctx := t.Context()
	classes := int32(2)
	if err := s.SetPasswordPolicy(ctx, nil, &classes); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetPasswordPolicy(ctx); err != nil || got != (password.Policy{MinLength: 15, Classes: 2}) {
		t.Errorf("got %+v, %v", got, err)
	}
}

func ptr[T any](v T) *T { return &v }
