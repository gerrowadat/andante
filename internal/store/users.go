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

// CountUsers returns how many users there are.
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	count, err := s.queries.CountUsers(ctx)
	if err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return count, nil
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
		return setCheck(ctx, q, to)
	})
	if err != nil {
		return fmt.Errorf("rotate secret: %w", err)
	}
	return nil
}

// ForgetSecrets is for a lost secret. Feed tokens carry on working, because they're
// looked up by hash, but their URLs can't be shown again.
func (s *Store) ForgetSecrets(ctx context.Context, to *seal.Box) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.ForgetFeedTokenSecrets(ctx); err != nil {
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
