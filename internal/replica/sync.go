// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package replica

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	pageSize = 500
	// holdSeconds is how long a request for changes waits at the catalogue.
	holdSeconds = 25
	// A catalogue that's starting up is back in a moment, so the first retry is
	// quick. One that's down costs nothing but a stale copy, so after that there's no
	// reason to hurry.
	firstRetry = 250 * time.Millisecond
	lastRetry  = 30 * time.Second
)

// Options configure a Syncer.
type Options struct {
	Store    *store.Store
	Client   *Client
	Logger   *slog.Logger
	Registry prometheus.Registerer
	// Furthest returns the furthest position of a source's reads, and whether it
	// has any.
	Furthest func(ctx context.Context, sourceID int64) (int32, bool, error)
	// DemandInterval is how often to tell the catalogue where readers have got to.
	DemandInterval time.Duration
}

// Syncer keeps the copy of the catalogue current, and reports demand back.
type Syncer struct {
	opts Options

	applied  prometheus.Counter
	failures prometheus.Counter
	lastGood prometheus.Gauge

	mu   sync.Mutex
	sent map[int64]*int32 // What the catalogue was last told.
}

// New returns a Syncer.
func New(opts Options) *Syncer {
	factory := promauto.With(opts.Registry)
	return &Syncer{
		opts: opts,
		applied: factory.NewCounter(prometheus.CounterOpts{
			Name: "andante_sync_changes_total", Help: "Catalogue changes applied to the local copy.",
		}),
		failures: factory.NewCounter(prometheus.CounterOpts{
			Name: "andante_sync_failures_total", Help: "Failed attempts to fetch or apply catalogue changes.",
		}),
		lastGood: factory.NewGauge(prometheus.GaugeOpts{
			Name: "andante_sync_last_success_timestamp_seconds", Help: "When the copy was last known to be current.",
		}),
		sent: map[int64]*int32{},
	}
}

// pointer turns a nullable into what sqlc wants.
func pointer[T any](n nullable.Nullable[T]) *T {
	v, err := n.Get()
	if err != nil {
		return nil
	}
	return &v
}

func decode(change catalogue.Change) (store.Change, error) {
	out := store.Change{Revision: change.Revision, DeletedSource: change.DeletedSourceID}
	if s := change.Source; s != nil {
		config, err := s.Config.MarshalJSON()
		if err != nil {
			return out, fmt.Errorf("encode config of source %d: %w", s.ID, err)
		}
		strategy, err := s.Config.Discriminator()
		if err != nil {
			return out, fmt.Errorf("read strategy of source %d: %w", s.ID, err)
		}
		extra, err := json.Marshal(s.Extra)
		if err != nil {
			return out, fmt.Errorf("encode extra of source %d: %w", s.ID, err)
		}
		out.Source = &db.UpsertSourceParams{
			ID: s.ID, Slug: s.Slug, Title: s.Title, HomeUrl: s.HomeURL, DefaultKind: string(s.DefaultKind),
			ImageUrl: pointer(s.ImageURL), Extra: extra, Strategy: strategy, Config: config, State: string(s.State),
			DueAt: pointer(s.DueAt), LastCrawledAt: pointer(s.LastCrawledAt), LastError: pointer(s.LastError),
			CreatedAt: s.CreatedAt,
		}
	}
	if e := change.Entry; e != nil {
		if e.SourceID == nil {
			return out, fmt.Errorf("entry %d has no source", e.ID)
		}
		enclosures, err := json.Marshal(e.Enclosures)
		if err != nil {
			return out, fmt.Errorf("encode enclosures of entry %d: %w", e.ID, err)
		}
		extra, err := json.Marshal(e.Extra)
		if err != nil {
			return out, fmt.Errorf("encode extra of entry %d: %w", e.ID, err)
		}
		var duration pgtype.Interval
		if d := pointer(e.Duration); d != nil {
			total, err := d.TimeDuration()
			if err != nil {
				return out, fmt.Errorf("read duration of entry %d: %w", e.ID, err)
			}
			duration = pgtype.Interval{Microseconds: total.Microseconds(), Valid: true}
		}
		out.Entry = &db.UpsertEntryParams{
			ID: e.ID, SourceID: *e.SourceID, Seq: e.Seq, Kind: string(e.Kind), Url: pointer(e.URL), Guid: pointer(e.GUID),
			Title: pointer(e.Title), Summary: pointer(e.Summary), ContentHtml: pointer(e.ContentHTML), Images: e.Images,
			AltText: pointer(e.AltText), Enclosures: enclosures, Duration: duration, Extra: extra,
			OriginalDate: pointer(e.OriginalDate), DiscoveredAt: e.DiscoveredAt,
		}
	}
	return out, nil
}

// pull applies one page of changes, and returns how many there were. Two pulls can
// overlap: applying is in a transaction that skips what's already been applied.
func (s *Syncer) pull(ctx context.Context, wait int32) (int, error) {
	after, err := s.opts.Store.SyncRevision(ctx)
	if err != nil {
		return 0, err
	}
	changes, err := s.opts.Client.Changes(ctx, after, pageSize, wait)
	if err != nil {
		return 0, err
	}
	decoded := make([]store.Change, 0, len(changes))
	for _, change := range changes {
		c, err := decode(change)
		if err != nil {
			return 0, err
		}
		decoded = append(decoded, c)
	}
	if err := s.opts.Store.ApplyChanges(ctx, decoded); err != nil {
		return 0, err
	}
	s.applied.Add(float64(len(decoded)))
	return len(decoded), nil
}

// CatchUp applies everything the catalogue has now, so that a request that has
// just changed it can read its own write.
func (s *Syncer) CatchUp(ctx context.Context) error {
	for {
		n, err := s.pull(ctx, 0)
		if err != nil {
			return err
		}
		if n == 0 {
			s.lastGood.SetToCurrentTime()
			return nil
		}
	}
}

// PushDemand tells the catalogue how far readers have got through a source.
func (s *Syncer) PushDemand(ctx context.Context, sourceID int64) error {
	seq, has, err := s.opts.Furthest(ctx, sourceID)
	if err != nil {
		return err
	}
	var demand *int32
	if has {
		demand = &seq
	}

	s.mu.Lock()
	last, known := s.sent[sourceID]
	s.mu.Unlock()
	if known && same(last, demand) {
		return nil
	}
	if err := s.opts.Client.SetDemand(ctx, sourceID, demand); err != nil {
		return err
	}
	s.mu.Lock()
	s.sent[sourceID] = demand
	s.mu.Unlock()
	return nil
}

func same(a, b *int32) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Syncer) pushAllDemand(ctx context.Context) {
	ids, err := s.opts.Store.ListSourceIDs(ctx)
	if err != nil {
		s.opts.Logger.Warn("could not list sources for demand", "error", err)
		return
	}
	for _, id := range ids {
		if err := s.PushDemand(ctx, id); err != nil && ctx.Err() == nil {
			s.opts.Logger.Warn("could not report demand", "source", id, "error", err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// Run keeps the copy and the catalogue's demand current until ctx ends. A failure
// is logged and retried, because feeds work without the catalogue.
func (s *Syncer) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() {
		retry := firstRetry
		for ctx.Err() == nil {
			n, err := s.pull(ctx, holdSeconds)
			switch {
			case ctx.Err() != nil:
			case err != nil:
				s.failures.Inc()
				s.opts.Logger.Warn("could not sync with the catalogue", "error", err)
				sleep(ctx, retry)
				retry = min(retry*2, lastRetry)
				continue
			case n < pageSize:
				s.lastGood.SetToCurrentTime()
			}
			retry = firstRetry
		}
	})
	wg.Go(func() {
		for ctx.Err() == nil {
			s.pushAllDemand(ctx)
			sleep(ctx, s.opts.DemandInterval)
		}
	})
	wg.Wait()
}
