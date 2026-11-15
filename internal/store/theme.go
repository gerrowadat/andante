// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"

	"github.com/gerrowadat/andante/internal/store/db"
)

// GetTheme returns the name of the website's theme.
func (s *Store) GetTheme(ctx context.Context) (string, error) {
	name, err := s.queries.GetTheme(ctx)
	if err != nil {
		return "", fmt.Errorf("read settings: %w", err)
	}
	return name, nil
}

// SetTheme changes the website's theme. The caller has checked that there's a theme
// by that name.
func (s *Store) SetTheme(ctx context.Context, name string) error {
	if err := s.queries.SetTheme(ctx, name); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}

// SetUserTheme changes the theme the actor picked for the website, and returns
// them. An empty name means they follow the site's. The caller has checked that
// there's a theme by that name.
func (s *Store) SetUserTheme(ctx context.Context, actor Actor, name string) (db.User, error) {
	var theme *string
	if name != "" {
		theme = &name
	}
	user, err := s.queries.SetUserTheme(ctx, db.SetUserThemeParams{ID: actor.UserID, Theme: theme})
	if err != nil {
		return db.User{}, fmt.Errorf("set theme of user %d: %w", actor.UserID, translate(err))
	}
	return user, nil
}
