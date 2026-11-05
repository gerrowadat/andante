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

	// bootstrapLockID serialises token bootstrap across servers. It spells
	// "andante" in ASCII, to stand apart from other advisory locks.
	bootstrapLockID = 0x616e64616e7465

	bootstrapLabel = "bootstrap"
)

// BootstrapAPIToken makes sure the first API token exists. If api_tokens is
// empty it stores adminToken, or a generated token if adminToken is empty. It
// returns the raw token only when it generated one, for the caller to show once.
//
// The advisory lock is what stops two servers starting together on an empty
// table from each creating a token.
func (s *Store) BootstrapAPIToken(ctx context.Context, adminToken string) (generated string, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockAPITokenBootstrap(ctx, bootstrapLockID); err != nil {
			return err
		}
		count, err := q.CountAPITokens(ctx)
		if err != nil {
			return err
		}
		if count > 0 {
			return nil
		}

		token := adminToken
		if token == "" {
			if token, err = newToken(); err != nil {
				return err
			}
			generated = token
		}
		hash := sha256.Sum256([]byte(token))
		_, err = q.InsertAPIToken(ctx, db.InsertAPITokenParams{TokenHash: hash[:], Label: bootstrapLabel})
		return err
	})
	if err != nil {
		return "", fmt.Errorf("bootstrap API token: %w", err)
	}
	return generated, nil
}

// CreateAPIToken stores a new token and returns it with its raw value, which
// exists nowhere else.
func (s *Store) CreateAPIToken(ctx context.Context, label string) (db.ApiToken, string, error) {
	raw, err := newToken()
	if err != nil {
		return db.ApiToken{}, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.InsertAPIToken(ctx, db.InsertAPITokenParams{TokenHash: hash[:], Label: label})
	if err != nil {
		return db.ApiToken{}, "", fmt.Errorf("insert API token: %w", err)
	}
	return token, raw, nil
}

// ListAPITokens returns up to limit tokens with IDs above after.
func (s *Store) ListAPITokens(ctx context.Context, after int64, limit int32) ([]db.ApiToken, error) {
	tokens, err := s.queries.ListAPITokens(ctx, db.ListAPITokensParams{ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list API tokens: %w", err)
	}
	return tokens, nil
}

// RevokeAPIToken revokes a token. Revoking one already revoked succeeds. It
// returns ErrLastToken for the last active token: bootstrap only runs on an
// empty table, so nothing could then use the API to make another.
func (s *Store) RevokeAPIToken(ctx context.Context, id int64) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockAPITokenBootstrap(ctx, bootstrapLockID); err != nil {
			return err
		}
		token, err := q.GetAPIToken(ctx, id)
		if err != nil {
			return err
		}
		if token.RevokedAt != nil {
			return nil
		}
		active, err := q.CountActiveAPITokens(ctx)
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

// AuthenticateAPIToken reports whether raw is an active API token, and notes
// its use at most once a minute.
func (s *Store) AuthenticateAPIToken(ctx context.Context, raw string) (bool, error) {
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.GetActiveAPITokenByHash(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up API token: %w", err)
	}
	if subtle.ConstantTimeCompare(token.TokenHash, hash[:]) != 1 {
		return false, nil
	}
	if err := s.queries.TouchAPIToken(ctx, token.ID); err != nil {
		return false, fmt.Errorf("record API token use: %w", err)
	}
	return true, nil
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
