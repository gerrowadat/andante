// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

const (
	tokenBytes = 32

	// tokenLockID serialises revocations, so that two can't each see the other's
	// token as the one left. It spells "andante" in ASCII, to stand apart from other
	// advisory locks.
	tokenLockID = 0x616e64616e7465
)

func insertAPIToken(ctx context.Context, q *db.Queries, userID int64, label string) (db.ApiToken, string, error) {
	raw, err := newToken()
	if err != nil {
		return db.ApiToken{}, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	token, err := q.InsertAPIToken(ctx, db.InsertAPITokenParams{UserID: userID, TokenHash: hash[:], Label: label})
	if err != nil {
		return db.ApiToken{}, "", fmt.Errorf("insert API token: %w", err)
	}
	return token, raw, nil
}

// CreateAPIToken stores a new token for a user and returns it with its raw value,
// which exists nowhere else.
func (s *Store) CreateAPIToken(ctx context.Context, actor Actor, label string) (db.ApiToken, string, error) {
	return insertAPIToken(ctx, s.queries, actor.UserID, label)
}

// ListAPITokens returns up to limit of a user's tokens with IDs above after.
func (s *Store) ListAPITokens(ctx context.Context, actor Actor, after int64, limit int32) ([]db.ApiToken, error) {
	tokens, err := s.queries.ListAPITokens(ctx, db.ListAPITokensParams{UserID: actor.UserID, ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	return tokens, nil
}

// RevokeAPIToken revokes one of a user's tokens. Revoking one already revoked
// succeeds. It returns ErrLastToken for their last active token, which would lock
// them out until someone ran andante-server user reset.
func (s *Store) RevokeAPIToken(ctx context.Context, actor Actor, id int64) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockAPITokens(ctx, tokenLockID); err != nil {
			return err
		}
		token, err := q.GetAPIToken(ctx, db.GetAPITokenParams{ID: id, UserID: actor.UserID})
		if err != nil {
			return err
		}
		if token.RevokedAt != nil {
			return nil
		}
		active, err := q.CountActiveAPITokens(ctx, actor.UserID)
		if err != nil {
			return err
		}
		if active <= 1 {
			return ErrLastToken
		}
		return q.RevokeAPIToken(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("revoke API token %d: %w", id, translate(err))
	}
	return nil
}

// AuthenticateAPIToken returns who raw belongs to, if it's an active token of a user
// who isn't disabled, and notes its use at most once a minute.
func (s *Store) AuthenticateAPIToken(ctx context.Context, raw string) (Actor, bool, error) {
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.GetActiveAPITokenByHash(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return Actor{}, false, nil
	}
	if err != nil {
		return Actor{}, false, fmt.Errorf("look up API token: %w", err)
	}
	if subtle.ConstantTimeCompare(token.TokenHash, hash[:]) != 1 {
		return Actor{}, false, nil
	}
	if err := s.queries.TouchAPIToken(ctx, token.ID); err != nil {
		return Actor{}, false, fmt.Errorf("record API token use: %w", err)
	}
	return Actor{UserID: token.UserID, Admin: token.Role == RoleAdmin, Name: token.Name}, true, nil
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
