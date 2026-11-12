// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// BootstrapLife is how long a bootstrap token the server made works. Logs are often
// read by more people than can reach the database, so a token in them shouldn't last
// for ever. The next start after it runs out makes another.
const BootstrapLife = 24 * time.Hour

const sealBootstrap = "bootstrap-token"

// ErrHaveAdmin means there's already an active admin, so there's nothing to set up.
var ErrHaveAdmin = errors.New("there's already an admin")

// NeedsAdmin reports whether there's no active admin.
func (s *Store) NeedsAdmin(ctx context.Context) (bool, error) {
	admins, err := s.queries.CountActiveAdmins(ctx)
	if err != nil {
		return false, fmt.Errorf("count admins: %w", err)
	}
	return admins == 0, nil
}

// BootstrapToken returns the bootstrap token the server made, and when it runs out. It
// makes one if there's none or the last has run out. If there's an active admin, it
// deletes any there is and returns ErrHaveAdmin.
func (s *Store) BootstrapToken(ctx context.Context, now time.Time) (string, time.Time, error) {
	var (
		raw       string
		until     time.Time
		haveAdmin bool
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockUsers(ctx, usersLockID); err != nil {
			return err
		}
		admins, err := q.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if admins > 0 {
			// Returning an error here would roll the delete back.
			haveAdmin = true
			return q.DeleteBootstrapToken(ctx)
		}
		row, err := q.GetBootstrapToken(ctx)
		if err == nil && now.Before(row.CreatedAt.Add(BootstrapLife)) {
			plain, err := s.box.Open(row.Sealed, sealBootstrap)
			if err != nil {
				return err
			}
			raw, until = string(plain), row.CreatedAt.Add(BootstrapLife)
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if raw, err = newToken(); err != nil {
			return err
		}
		sealed, err := s.box.Seal([]byte(raw), sealBootstrap)
		if err != nil {
			return err
		}
		until = now.Add(BootstrapLife)
		return q.SetBootstrapToken(ctx, db.SetBootstrapTokenParams{Sealed: sealed, CreatedAt: now})
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("bootstrap token: %w", err)
	}
	if haveAdmin {
		return "", time.Time{}, ErrHaveAdmin
	}
	return raw, until, nil
}

// StoredBootstrapToken returns the bootstrap token the server made, if it hasn't run
// out, or ErrNotFound. Unlike BootstrapToken it never makes one.
func (s *Store) StoredBootstrapToken(ctx context.Context, now time.Time) (string, error) {
	row, err := s.queries.GetBootstrapToken(ctx)
	if err != nil {
		return "", fmt.Errorf("bootstrap token: %w", translate(err))
	}
	if !now.Before(row.CreatedAt.Add(BootstrapLife)) {
		return "", fmt.Errorf("bootstrap token: %w", ErrNotFound)
	}
	plain, err := s.box.Open(row.Sealed, sealBootstrap)
	if err != nil {
		return "", fmt.Errorf("bootstrap token: %w", err)
	}
	return string(plain), nil
}

// ForgetBootstrapToken deletes the bootstrap token the server made, if there is one.
func (s *Store) ForgetBootstrapToken(ctx context.Context) error {
	if err := s.queries.DeleteBootstrapToken(ctx); err != nil {
		return fmt.Errorf("forget bootstrap token: %w", err)
	}
	return nil
}

// bootstrapping runs create under the users lock if there's still no active admin, and
// otherwise returns ErrHaveAdmin. The name has to be free, because the bootstrap token
// makes a new account and never takes over one that's there.
func (s *Store) bootstrapping(ctx context.Context, name string, create func(*db.Queries) error) error {
	if !validName.MatchString(name) {
		return ErrBadName
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockUsers(ctx, usersLockID); err != nil {
			return err
		}
		admins, err := q.CountActiveAdmins(ctx)
		if err != nil {
			return err
		}
		if admins > 0 {
			return ErrHaveAdmin
		}
		_, err = q.GetUserByName(ctx, name)
		switch {
		case err == nil:
			return ErrConflict
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		return create(q)
	})
}

// CreateBootstrapInvite makes an invite for a new admin, while there's no active admin,
// and returns the raw token. It deletes any unused bootstrap invite, so that there's
// only ever one about. It's ErrHaveAdmin if there's an admin, and ErrConflict if the
// name is taken.
func (s *Store) CreateBootstrapInvite(ctx context.Context, name string, now time.Time) (db.Invite, string, error) {
	var (
		invite db.Invite
		raw    string
	)
	err := s.bootstrapping(ctx, name, func(q *db.Queries) error {
		if err := q.DeleteUnusedBootstrapInvites(ctx); err != nil {
			return err
		}
		var err error
		if raw, err = newToken(); err != nil {
			return err
		}
		hash := sha256.Sum256([]byte(raw))
		invite, err = q.InsertBootstrapInvite(ctx, db.InsertBootstrapInviteParams{
			TokenHash: hash[:], Name: &name, CreatedAt: now, ExpiresAt: now.Add(InviteLife),
		})
		return err
	})
	if err != nil {
		return db.Invite{}, "", fmt.Errorf("bootstrap invite %s: %w", name, translate(err))
	}
	return invite, raw, nil
}

// CreateBootstrapAdmin makes a new admin and their first API token, while there's no
// active admin, and forgets the bootstrap token. It returns the raw API token. Errors
// are as for CreateBootstrapInvite.
func (s *Store) CreateBootstrapAdmin(ctx context.Context, name string) (db.User, string, error) {
	var (
		user db.User
		raw  string
	)
	err := s.bootstrapping(ctx, name, func(q *db.Queries) error {
		var err error
		if user, err = q.InsertUser(ctx, db.InsertUserParams{Name: name, Role: RoleAdmin}); err != nil {
			return err
		}
		if _, raw, err = insertAPIToken(ctx, q, user.ID, "bootstrap"); err != nil {
			return err
		}
		if err := q.DeleteUnusedBootstrapInvites(ctx); err != nil {
			return err
		}
		return q.DeleteBootstrapToken(ctx)
	})
	if err != nil {
		return db.User{}, "", fmt.Errorf("bootstrap admin %s: %w", name, translate(err))
	}
	return user, raw, nil
}
