// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
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
		return q.InsertAPIToken(ctx, db.InsertAPITokenParams{TokenHash: hash[:], Label: bootstrapLabel})
	})
	if err != nil {
		return "", fmt.Errorf("bootstrap API token: %w", err)
	}
	return generated, nil
}

func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
