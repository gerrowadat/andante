// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/twofactor"
	"github.com/jackc/pgx/v5"
)

const sealTOTP = "totp"

// How a session logged in.
const (
	MethodPassword = "password"
	MethodOIDC     = "oidc"
)

// TOTP is where a user's two-factor is.
type TOTP struct {
	// Secret is empty if it was never set up.
	Secret  string
	Enabled bool
}

// StartTOTP keeps a new secret for a user who's setting two-factor up. It's
// ErrConflict if theirs is already on.
func (s *Store) StartTOTP(ctx context.Context, userID int64, secret string) error {
	sealed, err := s.box.Seal([]byte(secret), sealTOTP)
	if err != nil {
		return err
	}
	rows, err := s.queries.SetPendingTOTP(ctx, db.SetPendingTOTPParams{ID: userID, TotpSealed: sealed})
	if err != nil {
		return fmt.Errorf("start two-factor for user %d: %w", userID, err)
	}
	if rows == 0 {
		return fmt.Errorf("start two-factor for user %d: %w", userID, ErrConflict)
	}
	return nil
}

// GetTOTP returns a user's TOTP secret and whether it's on.
func (s *Store) GetTOTP(ctx context.Context, userID int64) (TOTP, error) {
	row, err := s.queries.GetTOTP(ctx, userID)
	if err != nil {
		return TOTP{}, fmt.Errorf("get two-factor of user %d: %w", userID, translate(err))
	}
	if row.TotpSealed == nil {
		return TOTP{}, nil
	}
	plain, err := s.box.Open(row.TotpSealed, sealTOTP)
	if err != nil {
		return TOTP{}, fmt.Errorf("open two-factor secret of user %d: %w", userID, err)
	}
	return TOTP{Secret: string(plain), Enabled: row.TotpEnabledAt != nil}, nil
}

// EnableTOTP turns on the secret StartTOTP kept, having been given a code for step,
// and replaces the user's recovery codes. It's ErrConflict if there's nothing to turn on.
func (s *Store) EnableTOTP(ctx context.Context, userID, step int64, recovery []string, now time.Time) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		rows, err := q.EnableTOTP(ctx, db.EnableTOTPParams{ID: userID, At: &now, Step: step})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrConflict
		}
		return replaceRecoveryCodes(ctx, q, userID, recovery)
	})
	if err != nil {
		return fmt.Errorf("turn on two-factor for user %d: %w", userID, err)
	}
	return nil
}

func replaceRecoveryCodes(ctx context.Context, q *db.Queries, userID int64, codes []string) error {
	if err := q.DeleteRecoveryCodes(ctx, userID); err != nil {
		return err
	}
	for _, code := range codes {
		if err := q.InsertRecoveryCode(ctx, db.InsertRecoveryCodeParams{UserID: userID, CodeHash: twofactor.HashRecovery(code)}); err != nil {
			return err
		}
	}
	return nil
}

// UseTOTPStep takes a code for step, and reports false if that step, or a later one,
// was already used. That's what stops a code working twice.
func (s *Store) UseTOTPStep(ctx context.Context, userID, step int64) (bool, error) {
	rows, err := s.queries.UseTOTPStep(ctx, db.UseTOTPStepParams{ID: userID, Step: step})
	if err != nil {
		return false, fmt.Errorf("use a two-factor code for user %d: %w", userID, err)
	}
	return rows == 1, nil
}

// UseRecoveryCode uses up one of a user's recovery codes, and reports whether it was
// one of theirs and unused.
func (s *Store) UseRecoveryCode(ctx context.Context, userID int64, code string, now time.Time) (bool, error) {
	rows, err := s.queries.UseRecoveryCode(ctx, db.UseRecoveryCodeParams{UserID: userID, CodeHash: twofactor.HashRecovery(code), At: &now})
	if err != nil {
		return false, fmt.Errorf("use a recovery code for user %d: %w", userID, err)
	}
	return rows == 1, nil
}

// RecoveryCodesLeft counts a user's unused recovery codes.
func (s *Store) RecoveryCodesLeft(ctx context.Context, userID int64) (int64, error) {
	n, err := s.queries.CountRecoveryCodes(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count recovery codes of user %d: %w", userID, err)
	}
	return n, nil
}

func clearTOTP(ctx context.Context, q *db.Queries, userID int64) error {
	if err := q.ClearTOTP(ctx, userID); err != nil {
		return err
	}
	return q.DeleteRecoveryCodes(ctx, userID)
}

// DisableTOTP turns a user's two-factor off and throws away their recovery codes.
func (s *Store) DisableTOTP(ctx context.Context, userID int64) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		return clearTOTP(ctx, s.queries.WithTx(tx), userID)
	})
	if err != nil {
		return fmt.Errorf("turn off two-factor for user %d: %w", userID, err)
	}
	return nil
}

// RequireTOTP reports whether everyone who logs in with a password needs two-factor.
func (s *Store) RequireTOTP(ctx context.Context) (bool, error) {
	on, err := s.queries.GetRequireTOTP(ctx)
	if err != nil {
		return false, fmt.Errorf("read settings: %w", err)
	}
	return on, nil
}

// SetRequireTOTP changes whether everyone who logs in with a password needs two-factor.
func (s *Store) SetRequireTOTP(ctx context.Context, on bool) error {
	if err := s.queries.SetRequireTOTP(ctx, on); err != nil {
		return fmt.Errorf("change settings: %w", err)
	}
	return nil
}

// Identity is a login at an OpenID provider.
type Identity struct {
	Issuer, Subject string
}

// ErrDisabled means the user exists but can't log in.
var ErrDisabled = errors.New("this user is disabled")

// LoginByIdentity logs in whoever an identity belongs to. It's ErrNotFound if it
// belongs to nobody, and ErrDisabled if they're disabled.
func (s *Store) LoginByIdentity(ctx context.Context, id Identity, client Client, now time.Time) (db.User, string, error) {
	user, err := s.queries.GetIdentityUser(ctx, db.GetIdentityUserParams{Issuer: id.Issuer, Subject: id.Subject})
	if err != nil {
		return db.User{}, "", fmt.Errorf("look up identity: %w", translate(err))
	}
	if user.DisabledAt != nil {
		return user, "", ErrDisabled
	}
	raw, err := insertSession(ctx, s.queries, user.ID, MethodOIDC, client, now)
	return user, raw, err
}

// maxNameTries is how many numbered names to try for a new user before giving up.
const maxNameTries = 100

// CreateIdentityUser makes a user for an identity nobody has, and logs them in. The
// user is called base, or base-2 and so on if that's taken, and never takes over
// someone who already has the name.
func (s *Store) CreateIdentityUser(ctx context.Context, base string, id Identity, client Client, now time.Time) (db.User, string, error) {
	if !validName.MatchString(base) {
		return db.User{}, "", ErrBadName
	}
	for i := 1; i <= maxNameTries; i++ {
		name := base
		if i > 1 {
			suffix := fmt.Sprintf("-%d", i)
			name = base[:min(len(base), 64-len(suffix))] + suffix
		}
		var (
			user db.User
			raw  string
		)
		err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
			q := s.queries.WithTx(tx)
			var err error
			if user, err = q.InsertUser(ctx, db.InsertUserParams{Name: name, Role: RoleUser}); err != nil {
				return err
			}
			if err := q.InsertIdentity(ctx, db.InsertIdentityParams{UserID: user.ID, Issuer: id.Issuer, Subject: id.Subject, CreatedAt: now}); err != nil {
				return err
			}
			raw, err = insertSession(ctx, q, user.ID, MethodOIDC, client, now)
			return err
		})
		if err == nil {
			return user, raw, nil
		}
		if err := translate(err); !errors.Is(err, ErrConflict) {
			return db.User{}, "", fmt.Errorf("create user for identity: %w", err)
		}
		// Either the name is taken, or the identity was linked in the meantime.
		if _, err := s.queries.GetIdentityUser(ctx, db.GetIdentityUserParams{Issuer: id.Issuer, Subject: id.Subject}); err == nil {
			return db.User{}, "", fmt.Errorf("create user for identity: %w", ErrConflict)
		}
	}
	return db.User{}, "", fmt.Errorf("create user for identity: no free name like %s: %w", base, ErrConflict)
}
