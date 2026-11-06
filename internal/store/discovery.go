// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// crawlLockID makes sure only one replica crawls. It spells "crawl" in ASCII.
const crawlLockID = 0x6372617771

// ErrDuplicateEntry means an entry repeats one already indexed, by URL or by
// content: a next chain that has cycled back on itself.
var ErrDuplicateEntry = errors.New("entry is already indexed")

// NewEntry is an entry to index. Its seq comes from its position in the batch.
type NewEntry struct {
	Kind        string
	URL         *string
	GUID        *string
	Title       *string
	Summary     *string
	ContentHTML *string
	Images      []string
	AltText     *string
	Enclosures  []byte
	Duration    time.Duration
	Extra       []byte
	Date        *time.Time
	ContentHash []byte
}

// EntryUpdate is new metadata for a known page. seq never changes.
type EntryUpdate struct {
	URL     string
	Title   *string
	Images  []string
	AltText *string
}

// Outcome is what a crawl pass decided about a source.
type Outcome struct {
	SourceID  int64
	Cursor    []byte
	State     string
	DueAt     *time.Time
	CrawledAt *time.Time
	Error     *string

	// Show or site metadata. With no Extra, the source's stay as they are.
	ImageURL *string
	Extra    []byte
}

// CrawlLock is held for as long as this replica does the crawling.
type CrawlLock struct {
	conn *pgxpool.Conn
}

// TryCrawlLock takes the crawl lock, or returns nil if another replica holds it.
// A session lock lives on one connection, so the lock owns that connection.
func (s *Store) TryCrawlLock(ctx context.Context) (*CrawlLock, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire connection: %w", err)
	}
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(crawlLockID)).Scan(&locked); err != nil {
		conn.Release()
		return nil, fmt.Errorf("take crawl lock: %w", err)
	}
	if !locked {
		conn.Release()
		return nil, nil //nolint:nilnil // No lock and no failure is a normal answer.
	}
	return &CrawlLock{conn: conn}, nil
}

// Held reports whether the lock's connection is still alive. The lock dies with
// it, so a replica that cannot tell must stop crawling.
func (l *CrawlLock) Held(ctx context.Context) bool {
	return l.conn.Ping(ctx) == nil
}

// Release gives the lock up. It unlocks explicitly: closing the connection frees
// the lock too, but only once Postgres has noticed, so the next attempt could still
// find it held. Closing is the fallback, for a connection that cannot answer.
func (l *CrawlLock) Release(ctx context.Context) {
	if _, err := l.conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", int64(crawlLockID)); err != nil {
		_ = l.conn.Conn().Close(ctx)
	}
	l.conn.Release()
}

// ListDueSources returns the sources the crawl loop should look at.
func (s *Store) ListDueSources(ctx context.Context, now time.Time) ([]db.Source, error) {
	sources, err := s.queries.ListDueSources(ctx, &now)
	if err != nil {
		return nil, fmt.Errorf("list due sources: %w", err)
	}
	return sources, nil
}

// EntryStats returns how many entries a source has, and its highest seq, which
// is -1 with none.
func (s *Store) EntryStats(ctx context.Context, sourceID int64) (count int64, maxSeq int32, err error) {
	stats, err := s.queries.EntryStats(ctx, sourceID)
	if err != nil {
		return 0, 0, fmt.Errorf("entry stats of source %d: %w", sourceID, err)
	}
	return stats.Count, stats.MaxSeq, nil
}

// SaveBatch indexes entries after the current end of a source, applies updates
// to known pages and records the outcome, all or nothing. It returns
// ErrDuplicateEntry if an entry is already indexed.
func (s *Store) SaveBatch(ctx context.Context, entries []NewEntry, updates []EntryUpdate, outcome Outcome) error {
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.queries.WithTx(tx)
		stats, err := q.EntryStats(ctx, outcome.SourceID)
		if err != nil {
			return err
		}
		for i, e := range entries {
			known, err := q.EntryKnown(ctx, db.EntryKnownParams{SourceID: outcome.SourceID, Url: e.URL, Guid: e.GUID, ContentHash: e.ContentHash})
			if err != nil {
				return err
			}
			if known {
				return fmt.Errorf("%w: entry %d of the batch", ErrDuplicateEntry, i)
			}
			err = q.InsertEntry(ctx, db.InsertEntryParams{
				SourceID:     outcome.SourceID,
				Seq:          stats.MaxSeq + 1 + int32(i), //nolint:gosec // A batch is a handful of entries.
				Kind:         e.Kind,
				Url:          e.URL,
				Guid:         e.GUID,
				Title:        e.Title,
				Summary:      e.Summary,
				ContentHtml:  e.ContentHTML,
				Images:       e.Images,
				AltText:      e.AltText,
				Enclosures:   e.Enclosures,
				Duration:     interval(e.Duration),
				Extra:        e.Extra,
				OriginalDate: e.Date,
				ContentHash:  e.ContentHash,
			})
			if err != nil {
				return err
			}
		}
		for _, u := range updates {
			err := q.UpdateEntryContent(ctx, db.UpdateEntryContentParams{
				SourceID: outcome.SourceID, Url: &u.URL, Title: u.Title, Images: u.Images, AltText: u.AltText,
			})
			if err != nil {
				return err
			}
		}
		if outcome.Extra != nil {
			err := q.UpdateSourceMetadata(ctx, db.UpdateSourceMetadataParams{
				ID: outcome.SourceID, ImageUrl: outcome.ImageURL, Extra: outcome.Extra,
			})
			if err != nil {
				return err
			}
		}
		return q.RecordSourceOutcome(ctx, outcomeParams(outcome))
	})
	if err != nil {
		return fmt.Errorf("save batch for source %d: %w", outcome.SourceID, err)
	}
	return nil
}

func interval(d time.Duration) pgtype.Interval {
	if d == 0 {
		return pgtype.Interval{}
	}
	return pgtype.Interval{Microseconds: d.Microseconds(), Valid: true}
}

// FindSourceByFeedURL returns the source reading a feed, or ErrNotFound.
func (s *Store) FindSourceByFeedURL(ctx context.Context, feedURL string) (db.Source, error) {
	source, err := s.queries.FindSourceByFeedURL(ctx, []byte(feedURL))
	if err != nil {
		return db.Source{}, fmt.Errorf("find source by feed: %w", translate(err))
	}
	return source, nil
}

// RecordOutcome records a crawl pass that indexed nothing.
func (s *Store) RecordOutcome(ctx context.Context, outcome Outcome) error {
	if err := s.queries.RecordSourceOutcome(ctx, outcomeParams(outcome)); err != nil {
		return fmt.Errorf("record outcome for source %d: %w", outcome.SourceID, err)
	}
	return nil
}

func outcomeParams(o Outcome) db.RecordSourceOutcomeParams {
	return db.RecordSourceOutcomeParams{
		ID:            o.SourceID,
		Cursor:        o.Cursor,
		State:         o.State,
		DueAt:         o.DueAt,
		LastCrawledAt: o.CrawledAt,
		LastError:     o.Error,
	}
}

// RequestCrawl marks a source due now, or returns ErrNotFound.
func (s *Store) RequestCrawl(ctx context.Context, id int64) error {
	rows, err := s.queries.RequestCrawl(ctx, id)
	if err != nil {
		return fmt.Errorf("request crawl of source %d: %w", id, err)
	}
	if rows == 0 {
		return fmt.Errorf("request crawl of source %d: %w", id, ErrNotFound)
	}
	return nil
}

// RecordFetch adds a request to the crawl log.
func (s *Store) RecordFetch(ctx context.Context, params db.InsertCrawlLogParams) error {
	if err := s.queries.InsertCrawlLog(ctx, params); err != nil {
		return fmt.Errorf("record fetch: %w", err)
	}
	return nil
}

// Validators returns the ETag and Last-Modified of the last good fetch of url,
// for a conditional request.
func (s *Store) Validators(ctx context.Context, sourceID int64, url string) (etag, lastModified *string, err error) {
	row, err := s.queries.LatestValidators(ctx, db.LatestValidatorsParams{SourceID: sourceID, Url: url})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read validators: %w", err)
	}
	return row.Etag, row.LastModified, nil
}
