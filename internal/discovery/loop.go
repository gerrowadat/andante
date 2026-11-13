// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/logring"
)

// batchSize is the most pages one source gets per pass, so that one long archive
// cannot keep the others waiting.
const batchSize = 10

const (
	stateIndexing = "indexing"
	stateTail     = "tail"
	statePaused   = "paused"
	stateError    = "error"
)

// FurthestFunc returns the furthest read position on a source, and whether it
// has any reads.
type FurthestFunc func(ctx context.Context, sourceID int64) (position int32, ok bool, err error)

// LoopOptions configure a Loop.
type LoopOptions struct {
	Store        *store.Store
	Registry     *Registry
	Fetcher      *Fetcher
	Metrics      *Metrics
	Logger       *slog.Logger
	Clock        Clock
	Lookahead    int
	TailInterval time.Duration
	PollInterval time.Duration
	// Furthest reports read positions. With none, every source counts as
	// having no reads.
	Furthest FurthestFunc
}

// Loop keeps sources indexed, lazily and politely.
type Loop struct {
	opts LoopOptions
}

// NewLoop returns a Loop.
func NewLoop(opts LoopOptions) *Loop {
	if opts.Clock == nil {
		opts.Clock = realClock{}
	}
	if opts.Furthest == nil {
		opts.Furthest = func(context.Context, int64) (int32, bool, error) { return 0, false, nil }
	}
	return &Loop{opts: opts}
}

// Run crawls until ctx ends, for as long as this replica holds the crawl lock. A
// second replica waits here and serves feeds.
func (l *Loop) Run(ctx context.Context) error {
	for {
		lock, err := l.opts.Store.TryCrawlLock(ctx)
		if err != nil {
			l.opts.Logger.Error("could not take the crawl lock", "error", err)
		}
		if lock != nil {
			l.crawl(ctx, lock)
			lock.Release(context.WithoutCancel(ctx))
		}
		if err := l.opts.Clock.Sleep(ctx, l.opts.PollInterval); err != nil {
			return nil //nolint:nilerr // The context ending is how the loop stops.
		}
	}
}

func (l *Loop) crawl(ctx context.Context, lock *store.CrawlLock) {
	for lock.Held(ctx) {
		worked, err := l.Tick(ctx)
		if err != nil && ctx.Err() == nil {
			l.opts.Logger.Error("crawl pass failed", "error", err)
		}
		if !worked {
			if err := l.opts.Clock.Sleep(ctx, l.opts.PollInterval); err != nil {
				return
			}
		}
	}
}

// Tick looks at every source once, and reports whether it did any work.
func (l *Loop) Tick(ctx context.Context) (bool, error) {
	sources, err := l.opts.Store.ListDueSources(ctx, l.opts.Clock.Now())
	if err != nil {
		return false, err
	}
	var (
		worked bool
		errs   []error
	)
	for _, s := range sources {
		did, err := l.process(ctx, s)
		worked = worked || did
		if err != nil {
			errs = append(errs, fmt.Errorf("source %s: %w", s.Slug, err))
		}
		if ctx.Err() != nil {
			break
		}
	}
	return worked, errors.Join(errs...)
}

// wanted returns how many entries a source should index now, and why. A source
// with a due_at is forced: something asked for this pass.
func (l *Loop) wanted(ctx context.Context, s db.Source) (int, string, error) {
	if s.DueAt != nil {
		if s.State == stateTail {
			return batchSize, "checking the live end for new entries", nil
		}
		return batchSize, "it was due, because a crawl was asked for or a wait ended", nil
	}
	count, maxSeq, err := l.opts.Store.EntryStats(ctx, s.ID)
	if err != nil {
		return 0, "", err
	}
	position, hasReads, err := l.opts.Furthest(ctx, s.ID)
	if err != nil {
		return 0, "", err
	}
	need := l.opts.Lookahead - int(count)
	why := fmt.Sprintf("nobody reads it, and it keeps %d entries ready but has %d", l.opts.Lookahead, count)
	if hasReads {
		need = int(position) + l.opts.Lookahead - int(maxSeq)
		why = fmt.Sprintf("the furthest read is at %d and it keeps %d entries ahead, but has up to %d", position, l.opts.Lookahead, maxSeq)
	}
	return min(max(need, 0), batchSize), why, nil
}

func (l *Loop) process(ctx context.Context, s db.Source) (bool, error) {
	limit, why, err := l.wanted(ctx, s)
	if err != nil {
		return false, err
	}
	if limit == 0 {
		return false, nil
	}
	l.event(ctx, s, Note{Level: slog.LevelInfo, Message: fmt.Sprintf("looking for up to %d entries: %s", limit, why)})

	src := Source{ID: s.ID, HomeURL: s.HomeUrl, DefaultKind: s.DefaultKind, Config: s.Config}
	strategy, err := l.opts.Registry.Get(s.Strategy)
	if err != nil {
		return true, l.fail(ctx, s, err)
	}
	batch, err := strategy.Next(ctx, &loggingFetcher{loop: l, source: s}, src, s.Cursor, limit)
	if err != nil {
		return true, l.fail(ctx, s, err)
	}
	for _, warning := range batch.Warnings {
		l.opts.Logger.Warn("extraction warning", "source", s.Slug, "warning", warning)
		l.event(ctx, s, Note{Level: slog.LevelWarn, Message: warning})
	}
	outcome, err := l.save(ctx, s, batch)
	if err != nil {
		return true, l.fail(ctx, s, err)
	}
	if len(batch.Entries) > 0 {
		l.opts.Logger.Info("indexed", "source", s.Slug, "entries", len(batch.Entries))
	}
	l.event(ctx, s, Note{Level: slog.LevelInfo, Message: describe(batch, outcome)})
	l.opts.Metrics.entries.WithLabelValues(s.Slug).Add(float64(len(batch.Entries)))
	return true, nil
}

// describe says what a pass did, for the crawl log.
func describe(batch Batch, outcome store.Outcome) string {
	var message string
	switch {
	case len(batch.Entries) > 0 && len(batch.Updates) > 0:
		message = fmt.Sprintf("indexed %d entries and updated %d", len(batch.Entries), len(batch.Updates))
	case len(batch.Entries) > 0:
		message = fmt.Sprintf("indexed %d entries", len(batch.Entries))
	case len(batch.Updates) > 0:
		message = fmt.Sprintf("updated %d entries", len(batch.Updates))
	default:
		message = "found nothing new"
	}
	switch {
	case outcome.State != stateTail:
	case outcome.DueAt != nil:
		message += "; at the live end, checking again at " + outcome.DueAt.UTC().Format(time.RFC3339)
	default:
		message += "; at the live end, and nobody reads it, so it waits until someone does"
	}
	return message
}

func (l *Loop) save(ctx context.Context, s db.Source, batch Batch) (store.Outcome, error) {
	now := l.opts.Clock.Now()
	outcome := store.Outcome{SourceID: s.ID, Cursor: batch.Cursor, State: stateIndexing, CrawledAt: &now}
	if batch.LiveEnd {
		// A source enters tail mode at the live end, and leaves it when new
		// entries appear, which is when LiveEnd stops being true. With nobody
		// reading it, it parks: no next check, until someone starts a read.
		_, hasReads, err := l.opts.Furthest(ctx, s.ID)
		if err != nil {
			return store.Outcome{}, err
		}
		outcome.State = stateTail
		if hasReads {
			due := now.Add(l.opts.TailInterval)
			outcome.DueAt = &due
		}
	}

	entries := make([]store.NewEntry, 0, len(batch.Entries))
	for _, e := range batch.Entries {
		enclosures, err := encode(e.Enclosures, "[]")
		if err != nil {
			return store.Outcome{}, err
		}
		extra, err := encode(e.Extra, "{}")
		if err != nil {
			return store.Outcome{}, err
		}
		entries = append(entries, store.NewEntry{
			Kind: e.Kind, URL: optional(e.URL), GUID: optional(e.GUID), Title: optional(e.Title), Summary: optional(e.Summary),
			ContentHTML: optional(e.ContentHTML), Images: images(e.Images), AltText: optional(e.AltText),
			Enclosures: enclosures, Duration: e.Duration, Extra: extra, Date: e.Date, ContentHash: e.ContentHash,
		})
	}
	if batch.Source != nil {
		extra, err := encode(batch.Source.Extra, "{}")
		if err != nil {
			return store.Outcome{}, err
		}
		outcome.ImageURL, outcome.Extra = optional(batch.Source.ImageURL), extra
	}
	updates := make([]store.EntryUpdate, 0, len(batch.Updates))
	for _, e := range batch.Updates {
		updates = append(updates, store.EntryUpdate{URL: e.URL, Title: optional(e.Title), Images: images(e.Images), AltText: optional(e.AltText)})
	}

	err := l.opts.Store.SaveBatch(ctx, entries, updates, outcome)
	if errors.Is(err, store.ErrDuplicateEntry) {
		return store.Outcome{}, fmt.Errorf("%w: %w", ErrCycle, err)
	}
	if err != nil {
		return store.Outcome{}, err
	}
	l.setState(s.Slug, outcome.State)
	return outcome, nil
}

// fail records what stopped a source. Waiting on a host is not a failure: the
// source keeps its state and tries again when the host allows it.
func (l *Loop) fail(ctx context.Context, s db.Source, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	now := l.opts.Clock.Now()
	message := err.Error()
	outcome := store.Outcome{SourceID: s.ID, State: stateError, CrawledAt: &now, Error: &message}

	var backoff *BackoffError
	if errors.As(err, &backoff) {
		outcome.State, outcome.DueAt = s.State, &backoff.Until
		l.opts.Logger.Warn("backing off", "source", s.Slug, "until", backoff.Until, "cause", backoff.Cause)
		l.event(ctx, s, Note{Level: slog.LevelWarn, Message: fmt.Sprintf(
			"waiting until %s before asking %s again: %s", backoff.Until.UTC().Format(time.RFC3339), backoff.Host, backoff.Cause)})
	} else {
		l.opts.Logger.Error("source stopped", "source", s.Slug, "error", err)
		l.event(ctx, s, Note{Level: slog.LevelError, Message: "stopped: " + err.Error()})
	}
	if recordErr := l.opts.Store.RecordOutcome(ctx, outcome); recordErr != nil {
		return recordErr
	}
	l.setState(s.Slug, outcome.State)
	return nil
}

// event adds a note to the source's crawl log. Losing one is not worth stopping
// the crawl for.
func (l *Loop) event(ctx context.Context, s db.Source, note Note) {
	err := l.opts.Store.RecordEvent(ctx, store.CrawlEvent{
		SourceID: s.ID, Level: logring.LevelName(note.Level), Message: note.Message, URL: note.URL, Page: note.Page,
	})
	if err != nil {
		l.opts.Logger.Warn("could not record a crawl event", "source", s.Slug, "error", err)
	}
}

func (l *Loop) setState(slug, state string) {
	for _, each := range []string{stateIndexing, stateTail, statePaused, stateError} {
		value := 0.0
		if each == state {
			value = 1
		}
		l.opts.Metrics.state.WithLabelValues(slug, each).Set(value)
	}
}

// encode turns a value that may be empty into the JSON a column wants.
func encode[T any](value T, empty string) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode entry: %w", err)
	}
	if string(encoded) == "null" {
		return []byte(empty), nil
	}
	return encoded, nil
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// images keeps the not-null column happy for a page with none.
func images(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// loggingFetcher adds what only the crawl loop wants: conditional requests from
// crawl_log, and a record of each request.
type loggingFetcher struct {
	loop   *Loop
	source db.Source
}

// Fetch implements PageFetcher.
func (f *loggingFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	return f.fetch(ctx, url, Validators{}, nil, nil)
}

// Revalidate implements PageFetcher.
func (f *loggingFetcher) Revalidate(ctx context.Context, url string) (*Response, error) {
	var validators Validators
	etag, lastModified, err := f.loop.opts.Store.Validators(ctx, f.source.ID, url)
	if err != nil {
		return nil, err
	}
	if etag != nil {
		validators.ETag = *etag
	}
	if lastModified != nil {
		validators.LastModified = *lastModified
	}
	return f.fetch(ctx, url, validators, etag, lastModified)
}

// Note implements PageFetcher. Below a warning, a note is only debug in the
// service's log, which has no use for every tail check.
func (f *loggingFetcher) Note(ctx context.Context, note Note) {
	level := note.Level
	if level < slog.LevelWarn {
		level = slog.LevelDebug
	}
	f.loop.opts.Logger.Log(ctx, level, note.Message, "source", f.source.Slug, "url", note.URL)
	f.loop.event(ctx, f.source, note)
}

// Head implements PageFetcher.
func (f *loggingFetcher) Head(ctx context.Context, url string) (*Response, error) {
	l := f.loop
	resp, err := l.opts.Fetcher.Head(ctx, url)
	label := errorLabel(err)
	if resp != nil {
		label = strconv.Itoa(resp.Status)
	}
	l.opts.Metrics.requests.WithLabelValues(f.source.Slug, label).Inc()
	return resp, err
}

func (f *loggingFetcher) fetch(ctx context.Context, url string, validators Validators, etag, lastModified *string) (*Response, error) {
	l := f.loop
	start := l.opts.Clock.Now()
	resp, fetchErr := l.opts.Fetcher.Fetch(ctx, url, validators)
	if ctx.Err() != nil {
		return resp, fetchErr
	}
	l.opts.Metrics.duration.Observe(l.opts.Clock.Now().Sub(start).Seconds())

	row := db.InsertCrawlLogParams{SourceID: f.source.ID, Url: &url}
	label := errorLabel(fetchErr)
	if resp != nil {
		status := int32(resp.Status) //nolint:gosec // HTTP status codes fit.
		row.Status = &status
		row.Etag, row.LastModified = optional(resp.ETag), optional(resp.LastModified)
		label = strconv.Itoa(resp.Status)
		// A 304 confirms the validators in use, so they stay the latest.
		if resp.NotModified {
			row.Etag, row.LastModified = etag, lastModified
			ok := int32(200)
			row.Status = &ok
		}
	}
	if fetchErr != nil {
		message := fetchErr.Error()
		row.Error = &message
		var statusErr *StatusError
		if errors.As(fetchErr, &statusErr) {
			status := int32(statusErr.Status) //nolint:gosec // HTTP status codes fit.
			row.Status = &status
		}
	}
	l.opts.Metrics.requests.WithLabelValues(f.source.Slug, label).Inc()
	if err := l.opts.Store.RecordFetch(ctx, row); err != nil {
		l.opts.Logger.Warn("could not record a fetch", "source", f.source.Slug, "error", err)
	}
	return resp, fetchErr
}

// errorLabel keeps the status label bounded.
func errorLabel(err error) string {
	var (
		statusErr  *StatusError
		backoffErr *BackoffError
	)
	switch {
	case errors.As(err, &statusErr):
		return strconv.Itoa(statusErr.Status)
	case errors.As(err, &backoffErr):
		return "backoff"
	case errors.Is(err, ErrDisallowed):
		return "disallowed"
	default:
		return "error"
	}
}
