// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"

	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/store/db"
)

// GetPasswordPolicy returns what a new password has to be.
func (s *Store) GetPasswordPolicy(ctx context.Context) (password.Policy, error) {
	row, err := s.queries.GetPasswordPolicy(ctx)
	if err != nil {
		return password.Policy{}, fmt.Errorf("read settings: %w", err)
	}
	return password.Policy{
		MinLength: int(row.PasswordMinLength), NonLetters: int(row.PasswordNonLetters), Classes: int(row.PasswordClasses),
	}, nil
}

// PolicyChange is a change to the password policy. A nil leaves that part as it is.
type PolicyChange struct {
	MinLength, NonLetters, Classes *int32
}

// SetPasswordPolicy changes what a new password has to be. A policy out of range,
// counting the parts it leaves alone, is password.ErrBadPolicy.
func (s *Store) SetPasswordPolicy(ctx context.Context, change PolicyChange) error {
	// Two admins changing it at once can lose one change, but never store a policy that
	// doesn't make sense: the table checks the parts against each other too.
	p, err := s.GetPasswordPolicy(ctx)
	if err != nil {
		return err
	}
	for _, c := range []struct {
		to   *int32
		part *int
	}{{change.MinLength, &p.MinLength}, {change.NonLetters, &p.NonLetters}, {change.Classes, &p.Classes}} {
		if c.to != nil {
			*c.part = int(*c.to)
		}
	}
	if err := p.Validate(); err != nil {
		return err
	}
	err = s.queries.SetPasswordPolicy(ctx, db.SetPasswordPolicyParams{
		PasswordMinLength: int32(p.MinLength), PasswordNonLetters: int32(p.NonLetters), PasswordClasses: int32(p.Classes), //nolint:gosec // Validated.
	})
	if err != nil {
		return fmt.Errorf("change settings: %w", err)
	}
	return nil
}
