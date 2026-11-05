// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Errors callers can act on.
var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrLastToken = errors.New("last active API token")
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
)

// translate turns database errors into the store's own.
func translate(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %w", ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == pgUniqueViolation || pgErr.Code == pgForeignKeyViolation) {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}

// CreateSource stores a new source.
func (s *Store) CreateSource(ctx context.Context, params db.InsertSourceParams) (db.Source, error) {
	source, err := s.queries.InsertSource(ctx, params)
	if err != nil {
		return db.Source{}, fmt.Errorf("insert source: %w", translate(err))
	}
	return source, nil
}

// GetSource returns a source, or ErrNotFound.
func (s *Store) GetSource(ctx context.Context, id int64) (db.Source, error) {
	source, err := s.queries.GetSource(ctx, id)
	if err != nil {
		return db.Source{}, fmt.Errorf("get source %d: %w", id, translate(err))
	}
	return source, nil
}

// ListSources returns up to limit sources with IDs above after.
func (s *Store) ListSources(ctx context.Context, after int64, limit int32) ([]db.Source, error) {
	sources, err := s.queries.ListSources(ctx, db.ListSourcesParams{ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	return sources, nil
}

// UpdateSource applies change to a source in a transaction that holds its row,
// so that the read, the merge and the write cannot interleave with another
// update. A change that returns an error updates nothing.
func (s *Store) UpdateSource(ctx context.Context, id int64, change func(*db.UpdateSourceParams) error) (db.Source, error) {
	var updated db.Source
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		current, err := q.GetSourceForUpdate(ctx, id)
		if err != nil {
			return err
		}
		params := db.UpdateSourceParams{
			ID:          id,
			Title:       current.Title,
			HomeUrl:     current.HomeUrl,
			DefaultKind: current.DefaultKind,
			ImageUrl:    current.ImageUrl,
			Strategy:    current.Strategy,
			Config:      current.Config,
			State:       current.State,
		}
		if err := change(&params); err != nil {
			return err
		}
		updated, err = q.UpdateSource(ctx, params)
		return err
	})
	if err != nil {
		return db.Source{}, fmt.Errorf("update source %d: %w", id, translate(err))
	}
	return updated, nil
}

// DeleteSource deletes a source and its entries. It returns ErrConflict while a
// read uses the source.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	rows, err := s.queries.DeleteSource(ctx, id)
	if err != nil {
		return fmt.Errorf("delete source %d: %w", id, translate(err))
	}
	if rows == 0 {
		return fmt.Errorf("delete source %d: %w", id, ErrNotFound)
	}
	return nil
}

// ListEntries returns up to limit of a source's entries with seq above after, or
// ErrNotFound if the source does not exist.
func (s *Store) ListEntries(ctx context.Context, sourceID int64, after, limit int32) ([]db.Entry, error) {
	if _, err := s.GetSource(ctx, sourceID); err != nil {
		return nil, err
	}
	entries, err := s.queries.ListEntries(ctx, db.ListEntriesParams{SourceID: sourceID, Seq: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list entries of source %d: %w", sourceID, err)
	}
	return entries, nil
}
