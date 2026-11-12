// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/gerrowadat/andante/internal/seal"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// Roles a user can have.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

const (
	sealCheck     = "secret-check"
	sealFeedToken = "feed-token"
	checkValue    = "andante"
)

// ErrWrongSecret means --secret isn't the one this database was set up with.
var ErrWrongSecret = errors.New("--secret is not the one this database was set up with")

// Actor is who a request is for. Everything a person owns goes through a method that
// takes one, so that there's no way to fetch a read without saying whose it is.
type Actor struct {
	UserID int64
	Admin  bool
	Name   string // For the audit log. It decides nothing.
}

// Holder is the actor for code that's already established the right to a read, by a
// feed token or because it's the system itself. It's not for anything a user asked
// for by ID.
var Holder = Actor{Admin: true}

// validName is what a user can be called. Logging in folds names to lower case.
var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidName says whether a user could be called name.
func ValidName(name string) bool { return validName.MatchString(name) }

// ErrBadName means a name is no good.
var ErrBadName = errors.New("a name is 1 to 64 lower-case letters, digits, dots, dashes or underscores")

// CreateUser adds a user and their first API token, and returns the raw token.
func (s *Store) CreateUser(ctx context.Context, name, role string) (db.User, string, error) {
	if !validName.MatchString(name) {
		return db.User{}, "", ErrBadName
	}
	var (
		user db.User
		raw  string
	)
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		if user, err = q.InsertUser(ctx, db.InsertUserParams{Name: name, Role: role}); err != nil {
			return err
		}
		_, raw, err = insertAPIToken(ctx, q, user.ID, "first token")
		return err
	})
	if err != nil {
		return db.User{}, "", fmt.Errorf("create user: %w", translate(err))
	}
	return user, raw, nil
}

// GetUser returns a user, or ErrNotFound.
func (s *Store) GetUser(ctx context.Context, id int64) (db.User, error) {
	user, err := s.queries.GetUser(ctx, id)
	if err != nil {
		return db.User{}, fmt.Errorf("get user %d: %w", id, translate(err))
	}
	return user, nil
}

// ResetUser is for an account that's lost or compromised. It revokes all of a user's
// API tokens, ends their sessions, clears their password and issues a new token. It's
// what the command line is for, and no API reaches it.
func (s *Store) ResetUser(ctx context.Context, name string) (string, error) {
	var raw string
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		user, err := q.GetUserByName(ctx, name)
		if err != nil {
			return err
		}
		if err := q.RevokeAllAPITokens(ctx, user.ID); err != nil {
			return err
		}
		now := time.Now()
		if err := q.RevokeAllSessions(ctx, db.RevokeAllSessionsParams{UserID: user.ID, At: &now}); err != nil {
			return err
		}
		if err := q.ClearPassword(ctx, user.ID); err != nil {
			return err
		}
		if err := clearTOTP(ctx, q, user.ID); err != nil {
			return err
		}
		_, raw, err = insertAPIToken(ctx, q, user.ID, "reset")
		return err
	})
	if err != nil {
		return "", fmt.Errorf("reset user %s: %w", name, translate(err))
	}
	return raw, nil
}

// UseBox sets what feed tokens are sealed with, and checks that it's the right one. On
// a new database it sets the check. It returns ErrWrongSecret if it doesn't match.
func (s *Store) UseBox(ctx context.Context, box *seal.Box) error {
	sealed, err := s.queries.SecretCheck(ctx)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		check, err := box.Seal([]byte(checkValue), sealCheck)
		if err != nil {
			return err
		}
		if err := s.queries.SetSecretCheck(ctx, check); err != nil {
			return fmt.Errorf("record the secret check: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read the secret check: %w", err)
	default:
		if _, err := box.Open(sealed, sealCheck); err != nil {
			return ErrWrongSecret
		}
	}
	s.box = box
	return nil
}

// RotateSecret reseals everything from one secret to another, all or nothing.
func (s *Store) RotateSecret(ctx context.Context, from, to *seal.Box) error {
	sealed, err := s.queries.SecretCheck(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("read the secret check: %w", err)
	}
	if err == nil {
		if _, err := from.Open(sealed, sealCheck); err != nil {
			return ErrWrongSecret
		}
	}
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		rows, err := q.ListSealedFeedTokens(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			plain, err := from.Open(row.TokenSealed, sealFeedToken)
			if err != nil {
				return fmt.Errorf("feed token %d: %w", row.ID, err)
			}
			again, err := to.Seal(plain, sealFeedToken)
			if err != nil {
				return err
			}
			if err := q.ResealFeedToken(ctx, db.ResealFeedTokenParams{ID: row.ID, TokenSealed: again}); err != nil {
				return err
			}
		}
		secrets, err := q.ListSealedTOTP(ctx)
		if err != nil {
			return err
		}
		for _, row := range secrets {
			plain, err := from.Open(row.TotpSealed, sealTOTP)
			if err != nil {
				return fmt.Errorf("two-factor secret of user %d: %w", row.ID, err)
			}
			again, err := to.Seal(plain, sealTOTP)
			if err != nil {
				return err
			}
			if err := q.ResealTOTP(ctx, db.ResealTOTPParams{ID: row.ID, TotpSealed: again}); err != nil {
				return err
			}
		}
		// A bootstrap token the server made is only a convenience, and the next start
		// makes another.
		if err := q.DeleteBootstrapToken(ctx); err != nil {
			return err
		}
		return setCheck(ctx, q, to)
	})
	if err != nil {
		return fmt.Errorf("rotate secret: %w", err)
	}
	return nil
}

// ForgetSecrets is for a lost secret. Feed tokens carry on working, because they're
// looked up by hash, but their URLs can't be shown again. Two-factor is turned off for
// everyone, because a code can't be checked without the secret.
func (s *Store) ForgetSecrets(ctx context.Context, to *seal.Box) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.ForgetFeedTokenSecrets(ctx); err != nil {
			return err
		}
		if err := q.ForgetTOTP(ctx); err != nil {
			return err
		}
		if err := q.ForgetRecoveryCodes(ctx); err != nil {
			return err
		}
		if err := q.DeleteBootstrapToken(ctx); err != nil {
			return err
		}
		return setCheck(ctx, q, to)
	})
	if err != nil {
		return fmt.Errorf("forget secrets: %w", err)
	}
	return nil
}

func setCheck(ctx context.Context, q *db.Queries, box *seal.Box) error {
	check, err := box.Seal([]byte(checkValue), sealCheck)
	if err != nil {
		return err
	}
	return q.SetSecretCheck(ctx, check)
}

// ErrLastAdmin means a change would leave no active admin, and nobody could then
// manage users without the command line.
var ErrLastAdmin = errors.New("there would be no active admin left")

// ErrDeleteSelf means someone tried to delete their own account.
var ErrDeleteSelf = errors.New("you can't delete yourself")

// usersLockID serialises changes to who's an admin, so that two admins can't each
// demote the other. It spells "users" in ASCII.
const usersLockID = 0x7573657273

// ListUsers returns up to limit users with IDs above after.
func (s *Store) ListUsers(ctx context.Context, after int64, limit int32) ([]db.User, error) {
	users, err := s.queries.ListUsers(ctx, db.ListUsersParams{ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

// UserChange is what can change about a user. Nil means leave alone.
type UserChange struct {
	Role     *string
	Disabled *bool
}

// UpdateUser changes a user's role or disables them, and ends their sessions, so that
// nothing carries on under the old role. It returns ErrLastAdmin rather than leave no
// active admin.
func (s *Store) UpdateUser(ctx context.Context, id int64, change UserChange, now time.Time) (db.User, error) {
	var updated db.User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockUsers(ctx, usersLockID); err != nil {
			return err
		}
		user, err := q.GetUserForUpdate(ctx, id)
		if err != nil {
			return err
		}
		params := db.UpdateUserParams{ID: id, Role: user.Role, DisabledAt: user.DisabledAt}
		if change.Role != nil {
			params.Role = *change.Role
		}
		if change.Disabled != nil {
			switch {
			case *change.Disabled && user.DisabledAt == nil:
				params.DisabledAt = &now
			case !*change.Disabled:
				params.DisabledAt = nil
			}
		}
		wasAdmin := user.Role == RoleAdmin && user.DisabledAt == nil
		stillAdmin := params.Role == RoleAdmin && params.DisabledAt == nil
		if wasAdmin && !stillAdmin {
			admins, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		if updated, err = q.UpdateUser(ctx, params); err != nil {
			return err
		}
		if params.Role != user.Role || params.DisabledAt != nil {
			return q.RevokeAllSessions(ctx, db.RevokeAllSessionsParams{UserID: id, At: &now})
		}
		return nil
	})
	if err != nil {
		return db.User{}, fmt.Errorf("update user %d: %w", id, translate(err))
	}
	return updated, nil
}

// DeleteUser deletes a user with everything they own: reads, with their history and
// feed tokens, API tokens, sessions and invites.
func (s *Store) DeleteUser(ctx context.Context, actor Actor, id int64) error {
	if actor.UserID == id {
		return ErrDeleteSelf
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockUsers(ctx, usersLockID); err != nil {
			return err
		}
		user, err := q.GetUserForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if user.Role == RoleAdmin && user.DisabledAt == nil {
			admins, err := q.CountActiveAdmins(ctx)
			if err != nil {
				return err
			}
			if admins <= 1 {
				return ErrLastAdmin
			}
		}
		if err := q.DeleteReadsOfUser(ctx, id); err != nil {
			return err
		}
		if err := q.DeleteAPITokensOfUser(ctx, id); err != nil {
			return err
		}
		return q.DeleteUser(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("delete user %d: %w", id, translate(err))
	}
	return nil
}
