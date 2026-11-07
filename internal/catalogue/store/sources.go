// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/gerrowadat/andante/internal/canon"
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

// CreateSource stores a new source, working out its keys. It returns ErrConflict if the
// slug or the canonical key is taken.
func (s *Store) CreateSource(ctx context.Context, params db.InsertSourceParams) (db.Source, error) {
	key, err := canon.Key(params.Strategy, params.Config)
	if err != nil {
		return db.Source{}, fmt.Errorf("insert source: %w", err)
	}
	params.CanonicalKey, params.HomeKey = key, canon.URL(params.HomeUrl)
	source, err := s.queries.InsertSource(ctx, params)
	if err != nil {
		return db.Source{}, fmt.Errorf("insert source: %w", translate(err))
	}
	return source, nil
}

// GetSourceByKey returns the source with a canonical key, or ErrNotFound.
func (s *Store) GetSourceByKey(ctx context.Context, key string) (db.Source, error) {
	source, err := s.queries.GetSourceByKey(ctx, key)
	if err != nil {
		return db.Source{}, fmt.Errorf("get source by key: %w", translate(err))
	}
	return source, nil
}

// GetSourceByHome returns the first source with this home page, or ErrNotFound.
func (s *Store) GetSourceByHome(ctx context.Context, address string) (db.Source, error) {
	source, err := s.queries.GetSourceByHomeKey(ctx, canon.URL(address))
	if err != nil {
		return db.Source{}, fmt.Errorf("get source by home: %w", translate(err))
	}
	return source, nil
}

// SimilarSources returns the IDs of sources with this title or home page. It's for
// asking a person, because neither proves that two sources are the same serial.
func (s *Store) SimilarSources(ctx context.Context, title, home string) ([]int64, error) {
	ids, err := s.queries.ListSimilarSources(ctx, db.ListSimilarSourcesParams{Title: title, HomeKey: canon.URL(home)})
	if err != nil {
		return nil, fmt.Errorf("list similar sources: %w", err)
	}
	return ids, nil
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
		if params.CanonicalKey, err = canon.Key(params.Strategy, params.Config); err != nil {
			return err
		}
		params.HomeKey = canon.URL(params.HomeUrl)
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
	var rows int64
	err = s.locked(ctx, func(q *db.Queries) error {
		var err error
		rows, err = q.DeleteSource(ctx, id)
		return err
	})
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

// locked runs a write that fires the revision trigger, holding the revision lock from
// the start. A single UPDATE locks its row first and takes the revision lock in the
// trigger, which is the other way round to a transaction like SaveBatch. Two of those
// meeting on one source deadlock, so every such write takes the revision lock first.
func (s *Store) locked(ctx context.Context, write func(*db.Queries) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		if err := q.LockRevisions(ctx); err != nil {
			return err
		}
		return write(q)
	})
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
	if seq != nil {
		if err := s.locked(ctx, func(q *db.Queries) error { return q.WakeParked(ctx, id) }); err != nil {
			return fmt.Errorf("wake source %d: %w", id, err)
		}
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
