// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gerrowadat/andante/internal/catalogue/store/db"
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

// UpdateSource applies change to a source in a transaction that holds its row,
// so that the read, the merge and the write cannot interleave with another
// update. A change that returns an error updates nothing.
func (s *Store) UpdateSource(ctx context.Context, id int64, change func(*db.UpdateSourceParams) error) (db.Source, error) {
	var updated db.Source
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockRevisions(ctx); err != nil {
			return err
		}
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

// DeleteSource deletes a source and its entries. It returns ErrConflict while the
// source has demand, which means something is reading it.
func (s *Store) DeleteSource(ctx context.Context, id int64) error {
	demand, err := s.Demand(ctx, id)
	if err != nil {
		return err
	}
	if demand != nil {
		return fmt.Errorf("delete source %d: %w", id, ErrConflict)
	}
	rows, err := s.queries.DeleteSource(ctx, id)
	if err != nil {
		return fmt.Errorf("delete source %d: %w", id, translate(err))
	}
	if rows == 0 {
		return fmt.Errorf("delete source %d: %w", id, ErrNotFound)
	}
	return nil
}

// Demand returns how far readers have got through a source, or nil if nobody is
// reading it. It returns ErrNotFound if the source does not exist.
func (s *Store) Demand(ctx context.Context, id int64) (*int32, error) {
	demand, err := s.queries.Demand(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("demand of source %d: %w", id, translate(err))
	}
	return demand, nil
}

// Furthest is Demand in the shape the crawl loop wants.
func (s *Store) Furthest(ctx context.Context, id int64) (int32, bool, error) {
	demand, err := s.Demand(ctx, id)
	if err != nil || demand == nil {
		return 0, false, err
	}
	return *demand, true, nil
}

// SetDemand records how far readers have got. nil means nobody is reading.
func (s *Store) SetDemand(ctx context.Context, id int64, seq *int32) error {
	rows, err := s.queries.SetDemand(ctx, db.SetDemandParams{ID: id, DemandSeq: seq})
	if err != nil {
		return fmt.Errorf("set demand of source %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("set demand of source %d: %w", id, ErrNotFound)
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

// Change is one thing that changed in the catalogue. Exactly one of Source, Entry
// and DeletedSource is set.
type Change struct {
	Revision      int64
	Source        *db.Source
	Entry         *db.Entry
	DeletedSource *int64
}

// ChangesAfter returns up to limit changes after a revision, in order.
func (s *Store) ChangesAfter(ctx context.Context, after int64, limit int32) ([]Change, error) {
	sources, err := s.queries.SourcesChangedAfter(ctx, db.SourcesChangedAfterParams{Revision: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("changed sources: %w", err)
	}
	entries, err := s.queries.EntriesChangedAfter(ctx, db.EntriesChangedAfterParams{Revision: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("changed entries: %w", err)
	}
	deleted, err := s.queries.DeletedAfter(ctx, db.DeletedAfterParams{Revision: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("deleted sources: %w", err)
	}
	changes := make([]Change, 0, len(sources)+len(entries)+len(deleted))
	for i := range sources {
		changes = append(changes, Change{Revision: sources[i].Revision, Source: &sources[i]})
	}
	for i := range entries {
		changes = append(changes, Change{Revision: entries[i].Revision, Entry: &entries[i]})
	}
	for i := range deleted {
		changes = append(changes, Change{Revision: deleted[i].Revision, DeletedSource: &deleted[i].ID})
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Revision < changes[j].Revision })
	if len(changes) > int(limit) {
		changes = changes[:limit]
	}
	return changes, nil
}
