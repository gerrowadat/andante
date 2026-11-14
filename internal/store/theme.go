// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"
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
