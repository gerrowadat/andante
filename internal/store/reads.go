// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"fmt"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
)

// CreateRead stores a read with its first segment.
func (s *Store) CreateRead(ctx context.Context, read db.InsertReadParams, first db.InsertSegmentParams) (db.Read, error) {
	var created db.Read
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		var err error
		if created, err = q.InsertRead(ctx, read); err != nil {
			return err
		}
		first.ReadID = created.ID
		return q.InsertSegment(ctx, first)
	})
	if err != nil {
		return db.Read{}, fmt.Errorf("create read: %w", translate(err))
	}
	return created, nil
}

// GetRead returns a read, or ErrNotFound.
func (s *Store) GetRead(ctx context.Context, id int64) (db.Read, error) {
	read, err := s.queries.GetRead(ctx, id)
	if err != nil {
		return db.Read{}, fmt.Errorf("get read %d: %w", id, translate(err))
	}
	return read, nil
}

// ListReads returns up to limit reads with IDs above after.
func (s *Store) ListReads(ctx context.Context, after int64, limit int32) ([]db.Read, error) {
	reads, err := s.queries.ListReads(ctx, db.ListReadsParams{ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list reads: %w", err)
	}
	return reads, nil
}

// ListReadsOfSource returns every read of a source.
func (s *Store) ListReadsOfSource(ctx context.Context, sourceID int64) ([]db.Read, error) {
	reads, err := s.queries.ListReadsOfSource(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list reads of source %d: %w", sourceID, err)
	}
	return reads, nil
}

// UpdateRead changes what can change about a read.
func (s *Store) UpdateRead(ctx context.Context, params db.UpdateReadParams) (db.Read, error) {
	read, err := s.queries.UpdateRead(ctx, params)
	if err != nil {
		return db.Read{}, fmt.Errorf("update read %d: %w", params.ID, translate(err))
	}
	return read, nil
}

// DeleteRead deletes a read, its history and its feed tokens.
func (s *Store) DeleteRead(ctx context.Context, id int64) error {
	rows, err := s.queries.DeleteRead(ctx, id)
	if err != nil {
		return fmt.Errorf("delete read %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("delete read %d: %w", id, ErrNotFound)
	}
	return nil
}

// Segments returns a read's whole history, in order.
func (s *Store) Segments(ctx context.Context, readID int64) ([]db.ReadSegment, error) {
	segments, err := s.queries.ListSegments(ctx, readID)
	if err != nil {
		return nil, fmt.Errorf("list segments of read %d: %w", readID, err)
	}
	return segments, nil
}

// SegmentsPage returns up to limit of a read's segments with IDs above after, or
// ErrNotFound if the read does not exist.
func (s *Store) SegmentsPage(ctx context.Context, readID, after int64, limit int32) ([]db.ReadSegment, error) {
	if _, err := s.GetRead(ctx, readID); err != nil {
		return nil, err
	}
	segments, err := s.queries.ListSegmentsPage(ctx, db.ListSegmentsPageParams{ReadID: readID, ID: after, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("list segments of read %d: %w", readID, err)
	}
	return segments, nil
}

// EntryTimes returns what scheduling needs to know of a source's entries.
func (s *Store) EntryTimes(ctx context.Context, sourceID int64) ([]db.ListEntryTimesRow, error) {
	rows, err := s.queries.ListEntryTimes(ctx, sourceID)
	if err != nil {
		return nil, fmt.Errorf("list entry times of source %d: %w", sourceID, err)
	}
	return rows, nil
}

// AppendSegment adds the segment build returns, in a transaction that locks the
// read's row. Concurrent adjustments therefore serialise, and each one sees the
// segment the one before it appended.
func (s *Store) AppendSegment(ctx context.Context, readID int64, build func(db.Read, []db.ReadSegment, []db.ListEntryTimesRow) (db.InsertSegmentParams, error)) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		read, err := q.GetReadForUpdate(ctx, readID)
		if err != nil {
			return err
		}
		segments, err := q.ListSegments(ctx, readID)
		if err != nil {
			return err
		}
		times, err := q.ListEntryTimes(ctx, read.SourceID)
		if err != nil {
			return err
		}
		segment, err := build(read, segments, times)
		if err != nil {
			return err
		}
		segment.ReadID = readID
		return q.InsertSegment(ctx, segment)
	})
	if err != nil {
		return fmt.Errorf("adjust read %d: %w", readID, translate(err))
	}
	return nil
}
