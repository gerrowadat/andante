// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"

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

// GetSource returns a source, or ErrNotFound.
func (s *Store) GetSource(ctx context.Context, id int64) (db.Source, error) {
	source, err := s.queries.GetSource(ctx, id)
	if err != nil {
		return db.Source{}, fmt.Errorf("get source %d: %w", id, translate(err))
	}
	return source, nil
}

// SourceFilter narrows a list of sources. Zero values match everything.
type SourceFilter struct {
	Query string // Part of a title or slug.
	Kind  string
}

// ListSources returns up to limit sources with IDs above after.
func (s *Store) ListSources(ctx context.Context, filter SourceFilter, after int64, limit int32) ([]db.Source, error) {
	sources, err := s.queries.ListSources(ctx, db.ListSourcesParams{After: after, Q: filter.Query, Kind: filter.Kind, PageSize: limit})
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	return sources, nil
}

// FindSourceByFeedURL returns the source reading a feed, or ErrNotFound.
func (s *Store) FindSourceByFeedURL(ctx context.Context, feedURL string) (db.Source, error) {
	source, err := s.queries.FindSourceByFeedURL(ctx, []byte(feedURL))
	if err != nil {
		return db.Source{}, fmt.Errorf("find source by feed: %w", translate(err))
	}
	return source, nil
}

// ListSourceIDs returns every source's ID.
func (s *Store) ListSourceIDs(ctx context.Context) ([]int64, error) {
	ids, err := s.queries.ListSourceIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list source IDs: %w", err)
	}
	return ids, nil
}

// CountReads returns how many reads use a source.
func (s *Store) CountReads(ctx context.Context, sourceID int64) (int64, error) {
	count, err := s.queries.CountReadsOfSource(ctx, sourceID)
	if err != nil {
		return 0, fmt.Errorf("count reads of source %d: %w", sourceID, err)
	}
	return count, nil
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

// Progress is how far indexing a source has got: how many entries there are, and
// the last of them, which is nil if there are none.
type Progress struct {
	Entries int64
	Last    *db.Entry
}

// SourceProgress returns how far indexing a source has got.
func (s *Store) SourceProgress(ctx context.Context, sourceID int64) (Progress, error) {
	count, err := s.queries.CountEntries(ctx, sourceID)
	if err != nil {
		return Progress{}, fmt.Errorf("count entries of source %d: %w", sourceID, err)
	}
	if count == 0 {
		return Progress{}, nil
	}
	last, err := s.queries.LastEntry(ctx, sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, nil
	}
	if err != nil {
		return Progress{}, fmt.Errorf("last entry of source %d: %w", sourceID, err)
	}
	return Progress{Entries: count, Last: &last}, nil
}

// Change is one change from the catalogue. Exactly one of Source, Entry and
// DeletedSource is set.
type Change struct {
	Revision      int64
	Source        *db.UpsertSourceParams
	Entry         *db.UpsertEntryParams
	DeletedSource *int64
}

// SyncRevision returns the last catalogue revision applied.
func (s *Store) SyncRevision(ctx context.Context) (int64, error) {
	revision, err := s.queries.PeekSyncRevision(ctx)
	if err != nil {
		return 0, fmt.Errorf("read sync revision: %w", err)
	}
	return revision, nil
}

// ApplyChanges applies changes and moves the sync revision on, all or nothing.
// Holding the sync_state row means two syncs take turns, and anything already
// applied is skipped.
//
// A deleted source that a read still uses stays. The catalogue refuses to delete a
// source with demand, so this takes a race, and a read shouldn't pay for it.
//
// Each source that changed is announced on SourceChannel when the transaction
// commits, for pages that show it as it changes.
func (s *Store) ApplyChanges(ctx context.Context, changes []Change) error {
	if len(changes) == 0 {
		return nil
	}
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		revision, err := q.SyncRevision(ctx)
		if err != nil {
			return err
		}
		var changed []int64
		for _, c := range changes {
			if c.Revision <= revision {
				continue
			}
			revision = c.Revision
			switch {
			case c.Source != nil:
				err = q.UpsertSource(ctx, *c.Source)
				changed = append(changed, c.Source.ID)
			case c.Entry != nil:
				err = q.UpsertEntry(ctx, *c.Entry)
				changed = append(changed, c.Entry.SourceID)
			case c.DeletedSource != nil:
				err = deleteSource(ctx, q, *c.DeletedSource)
				changed = append(changed, *c.DeletedSource)
			}
			if err != nil {
				return err
			}
		}
		// A crawl's batch is many changes to one source, and one notice covers them.
		slices.Sort(changed)
		for _, id := range slices.Compact(changed) {
			if err := q.NotifySourceChanged(ctx, id); err != nil {
				return err
			}
		}
		return q.SetSyncRevision(ctx, revision)
	})
	if err != nil {
		return fmt.Errorf("apply changes: %w", err)
	}
	return nil
}

func deleteSource(ctx context.Context, q *db.Queries, id int64) error {
	reads, err := q.CountReadsOfSource(ctx, id)
	if err != nil || reads > 0 {
		return err
	}
	if err := q.DeleteEntriesOfSource(ctx, id); err != nil {
		return err
	}
	return q.DeleteSource(ctx, id)
}
