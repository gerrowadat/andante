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
	return password.Policy{MinLength: int(row.PasswordMinLength), Classes: int(row.PasswordClasses)}, nil
}

// SetPasswordPolicy changes what a new password has to be. A nil leaves that setting as
// it is, and one out of range is password.ErrBadPolicy.
func (s *Store) SetPasswordPolicy(ctx context.Context, minLength, classes *int32) error {
	check := password.Policy{MinLength: password.Shortest}
	if minLength != nil {
		check.MinLength = int(*minLength)
	}
	if classes != nil {
		check.Classes = int(*classes)
	}
	if err := check.Validate(); err != nil {
		return err
	}
	if err := s.queries.SetPasswordPolicy(ctx, db.SetPasswordPolicyParams{PasswordMinLength: minLength, PasswordClasses: classes}); err != nil {
		return fmt.Errorf("change settings: %w", err)
	}
	return nil
}
