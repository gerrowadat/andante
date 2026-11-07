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

// CreateFeedToken stores a new token on a read the actor can reach, and returns it
// with its raw value. It's looked up by hash, and kept sealed so that its URL can be
// shown again.
func (s *Store) CreateFeedToken(ctx context.Context, actor Actor, readID int64, role, label string) (db.FeedToken, string, error) {
	if _, err := s.GetRead(ctx, actor, readID); err != nil {
		return db.FeedToken{}, "", err
	}
	raw, err := newToken()
	if err != nil {
		return db.FeedToken{}, "", err
	}
	if s.box == nil {
		return db.FeedToken{}, "", errors.New("the store has no secret to seal tokens with")
	}
	sealed, err := s.box.Seal([]byte(raw), sealFeedToken)
	if err != nil {
		return db.FeedToken{}, "", err
	}
	hash := sha256.Sum256([]byte(raw))
	token, err := s.queries.InsertFeedToken(ctx, db.InsertFeedTokenParams{
		ReadID: readID, TokenHash: hash[:], TokenSealed: sealed, Role: role, Label: label,
	})
	if err != nil {
		return db.FeedToken{}, "", fmt.Errorf("insert feed token: %w", translate(err))
	}
	return token, raw, nil
}

// RawFeedToken opens a token's sealed value. It returns "" if it has none, because
// the secret was lost.
func (s *Store) RawFeedToken(token db.FeedToken) (string, error) {
	if token.TokenSealed == nil {
		return "", nil
	}
	if s.box == nil {
		return "", errors.New("the store has no secret to open tokens with")
	}
	raw, err := s.box.Open(token.TokenSealed, sealFeedToken)
	if err != nil {
		return "", fmt.Errorf("open feed token %d: %w", token.ID, err)
	}
	return string(raw), nil
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
// ErrNotFound if the actor can't reach the read.
func (s *Store) ListFeedTokens(ctx context.Context, actor Actor, readID, after int64, limit int32) ([]db.FeedToken, error) {
	if _, err := s.GetRead(ctx, actor, readID); err != nil {
		return nil, err
	}
	tokens, err := s.queries.ListFeedTokens(ctx, db.ListFeedTokensParams{ReadID: readID, ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list feed tokens: %w", err)
	}
	return tokens, nil
}

// RevokeFeedToken revokes a token on a read the actor can reach. Revoking one
// already revoked succeeds.
func (s *Store) RevokeFeedToken(ctx context.Context, actor Actor, id int64) error {
	rows, err := s.queries.RevokeFeedToken(ctx, db.RevokeFeedTokenParams{ID: id, Anyone: actor.Admin, UserID: actor.UserID})
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
