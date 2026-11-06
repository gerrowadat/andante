// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// CreateFeedToken stores a new token and returns it with its raw value, which
// exists nowhere else: only its hash is kept.
func (s *Store) CreateFeedToken(ctx context.Context, readID int64, role, label string) (db.FeedToken, string, error) {
	raw, err := newToken()
	if err != nil {
		return db.FeedToken{}, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.InsertFeedToken(ctx, db.InsertFeedTokenParams{ReadID: readID, TokenHash: hash[:], Role: role, Label: label})
	if err != nil {
		return db.FeedToken{}, "", fmt.Errorf("insert feed token: %w", translate(err))
	}
	return token, raw, nil
}

// LookupFeedToken returns the active token raw stands for, or ErrNotFound.
func (s *Store) LookupFeedToken(ctx context.Context, raw string) (db.FeedToken, error) {
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.GetActiveFeedTokenByHash(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return db.FeedToken{}, ErrNotFound
	}
	if err != nil {
		return db.FeedToken{}, fmt.Errorf("look up feed token: %w", err)
	}
	if subtle.ConstantTimeCompare(token.TokenHash, hash[:]) != 1 {
		return db.FeedToken{}, ErrNotFound
	}
	return token, nil
}

// TouchFeedToken notes a token's use, at most once an hour.
func (s *Store) TouchFeedToken(ctx context.Context, id int64) error {
	if err := s.queries.TouchFeedToken(ctx, id); err != nil {
		return fmt.Errorf("record feed token use: %w", err)
	}
	return nil
}

// ListFeedTokens returns up to limit of a read's tokens with IDs above after, or
// ErrNotFound if the read does not exist.
func (s *Store) ListFeedTokens(ctx context.Context, readID, after int64, limit int32) ([]db.FeedToken, error) {
	if _, err := s.GetRead(ctx, readID); err != nil {
		return nil, err
	}
	tokens, err := s.queries.ListFeedTokens(ctx, db.ListFeedTokensParams{ReadID: readID, ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list feed tokens: %w", err)
	}
	return tokens, nil
}

// RevokeFeedToken revokes a token. Revoking one already revoked succeeds.
func (s *Store) RevokeFeedToken(ctx context.Context, id int64) error {
	rows, err := s.queries.RevokeFeedToken(ctx, id)
	if err != nil {
		return fmt.Errorf("revoke feed token %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("revoke feed token %d: %w", id, ErrNotFound)
	}
	return nil
}

// EntriesInRange returns a source's entries from seq first to last, newest first.
func (s *Store) EntriesInRange(ctx context.Context, sourceID int64, first, last int32) ([]db.Entry, error) {
	entries, err := s.queries.ListEntriesInRange(ctx, db.ListEntriesInRangeParams{SourceID: sourceID, Seq: first, Seq_2: last})
	if err != nil {
		return nil, fmt.Errorf("list entries of source %d: %w", sourceID, err)
	}
	return entries, nil
}

// LatestSegmentID returns the ID of a read's last segment.
func (s *Store) LatestSegmentID(ctx context.Context, readID int64) (int64, error) {
	id, err := s.queries.LatestSegmentID(ctx, readID)
	if err != nil {
		return 0, fmt.Errorf("latest segment of read %d: %w", readID, err)
	}
	return id, nil
}
