// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package reads_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
)

const dailyAt7 = "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0"

type fixture struct {
	store   *store.Store
	clock   *sitetest.Clock
	service *reads.Service
	source  db.Source
}

func newFixture(t *testing.T, entries int) *fixture {
	t.Helper()
	st := storetest.New(t)
	clock := sitetest.NewClock()
	f := &fixture{store: st, clock: clock, service: reads.New(reads.Options{
		Store: st, DefaultTimezone: "UTC", MinDatedShare: 0.9, Now: clock.Now,
	})}
	source, err := st.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: "https://comic.example/", DefaultKind: "page",
		Strategy: "crawl", Config: []byte(`{"strategy":"crawl","start_url":"https://comic.example/1"}`), State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.source = source
	f.addEntries(t, 0, entries, true)
	return f
}

func (f *fixture) addEntries(t *testing.T, from, n int, dated bool) {
	t.Helper()
	for i := from; i < from+n; i++ {
		var date any
		if dated {
			date = time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*i)
		}
		_, err := f.store.Pool().Exec(t.Context(),
			"INSERT INTO entries (source_id, seq, kind, url, original_date, discovered_at) VALUES ($1, $2, 'page', $3, $4, $5)",
			f.source.ID, i, "https://comic.example/"+time.Duration(i).String(), date, f.clock.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) create(t *testing.T, p schedule.Pacing) reads.View {
	t.Helper()
	v, err := f.service.Create(t.Context(), reads.CreateParams{SourceID: f.source.ID, Name: "r", Pacing: p, Initial: 1})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func daily() schedule.RRulePacing {
	return schedule.RRulePacing{RRule: dailyAt7, TimeZone: "UTC", PerRelease: 1}
}

func (f *fixture) adjust(t *testing.T, id int64, a schedule.Adjustment) reads.View {
	t.Helper()
	v, err := f.service.Adjust(t.Context(), id, a)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCreateAndPosition(t *testing.T) {
	f := newFixture(t, 10)
	v := f.create(t, daily())
	if v.Position != 0 || v.Due != 0 || v.State != reads.StateActive {
		t.Errorf("new read: %+v", v)
	}
	if v.IndexEnd == nil || *v.IndexEnd != 9 {
		t.Errorf("index_end = %v", v.IndexEnd)
	}
	if v.NextRelease == nil || !v.NextRelease.After(f.clock.Now()) {
		t.Errorf("next release = %v", v.NextRelease)
	}

	f.clock.Advance(72 * time.Hour)
	got, err := f.service.Get(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Position != 3 {
		t.Errorf("position after three days = %d, want 3", got.Position)
	}
}

func TestStates(t *testing.T) {
	f := newFixture(t, 3)
	v := f.create(t, daily())
	paused := f.adjust(t, v.Read.ID, schedule.Pause{})
	if paused.State != reads.StatePaused {
		t.Errorf("state = %q, want paused", paused.State)
	}
	f.adjust(t, v.Read.ID, schedule.Resume{})
	f.clock.Advance(30 * 24 * time.Hour)
	done, err := f.service.Get(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != reads.StateCaughtUp {
		t.Errorf("state = %q, want caught-up", done.State)
	}
	if done.Due <= done.Position {
		t.Errorf("due %d should pass position %d", done.Due, done.Position)
	}
}

func TestStartSeqAndInitial(t *testing.T) {
	f := newFixture(t, 20)
	start := 5
	v, err := f.service.Create(t.Context(), reads.CreateParams{
		SourceID: f.source.ID, Name: "r", Pacing: daily(), StartSeq: &start, Initial: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v.Position != 7 || v.Read.StartSeq != 5 {
		t.Errorf("position %d, start %d", v.Position, v.Read.StartSeq)
	}
}

func TestBuffer(t *testing.T) {
	f := newFixture(t, 5)
	v, err := f.service.Create(t.Context(), reads.CreateParams{
		SourceID: f.source.ID, Name: "r", Pacing: daily(), Initial: 1, CatchUp: "buffer", Buffer: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(30 * 24 * time.Hour)
	got, err := f.service.Get(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Position != 2 {
		t.Errorf("position = %d, want two held back from 4", got.Position)
	}
}

func TestPersistsEachPacing(t *testing.T) {
	f := newFixture(t, 10)
	replay := schedule.ReplayPacing{Speed: 7, MinGap: time.Hour, MaxGap: 48 * time.Hour, DefaultGap: 24 * time.Hour}
	v := f.create(t, replay)
	rows, err := f.store.Segments(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reads.PacingOf(rows[0])
	if err != nil {
		t.Fatal(err)
	}
	if got != schedule.Pacing(replay) {
		t.Errorf("pacing = %+v, want %+v", got, replay)
	}
	if rows[0].Timezone != "UTC" {
		t.Errorf("timezone = %q, want the default", rows[0].Timezone)
	}
	paused := f.adjust(t, v.Read.ID, schedule.Pause{})
	rows, err = f.store.Segments(t.Context(), paused.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := reads.PacingOf(rows[1]); err != nil || p != schedule.Pacing(schedule.Paused{}) {
		t.Errorf("paused segment: %+v, %v", p, err)
	}
}

func TestPacingOfBrokenRows(t *testing.T) {
	for _, row := range []db.ReadSegment{{Pacing: "rrule"}, {Pacing: "replay"}, {Pacing: "other"}} {
		if _, err := reads.PacingOf(row); err == nil {
			t.Errorf("PacingOf(%q) succeeded", row.Pacing)
		}
	}
}

func TestReplayNeedsDates(t *testing.T) {
	f := newFixture(t, 0)
	f.addEntries(t, 0, 10, false)
	replay := schedule.ReplayPacing{Speed: 7}
	_, err := f.service.Create(t.Context(), reads.CreateParams{SourceID: f.source.ID, Name: "r", Pacing: replay, Initial: 1})
	if !errors.Is(err, reads.ErrTooFewDates) {
		t.Errorf("Create = %v, want ErrTooFewDates", err)
	}

	withGap := schedule.ReplayPacing{Speed: 7, DefaultGap: 24 * time.Hour}
	v := f.create(t, withGap)

	rrule := f.create(t, daily())
	if _, err := f.service.Adjust(t.Context(), rrule.Read.ID, schedule.Repace{Pacing: replay}); !errors.Is(err, reads.ErrTooFewDates) {
		t.Errorf("Adjust = %v, want ErrTooFewDates", err)
	}
	if v.Read.ID == 0 {
		t.Error("the read with a default_gap was not created")
	}
}

func TestCreateFailures(t *testing.T) {
	f := newFixture(t, 1)
	if _, err := f.service.Create(t.Context(), reads.CreateParams{SourceID: 9999, Name: "r", Pacing: daily()}); !errors.Is(err, reads.ErrSourceNotFound) {
		t.Errorf("missing source: %v", err)
	}
	bad := schedule.RRulePacing{RRule: "nope", TimeZone: "UTC", PerRelease: 1}
	if _, err := f.service.Create(t.Context(), reads.CreateParams{SourceID: f.source.ID, Name: "r", Pacing: bad}); !errors.Is(err, schedule.ErrInvalidPacing) {
		t.Errorf("bad rule: %v", err)
	}
}

func TestUpdate(t *testing.T) {
	f := newFixture(t, 3)
	v := f.create(t, daily())
	name := "renamed"
	updated, err := f.service.Update(t.Context(), v.Read.ID, reads.PatchParams{Name: &name, Config: json.RawMessage(`{"window":10}`)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Read.Name != name || !json.Valid(updated.Read.Config) {
		t.Errorf("%+v", updated.Read)
	}

	// Sending the values it already has is not a change.
	catchUp, buffer := "passthrough", 0
	if _, err := f.service.Update(t.Context(), v.Read.ID, reads.PatchParams{CatchUp: &catchUp, Buffer: &buffer}); err != nil {
		t.Errorf("no-op patch: %v", err)
	}
	buffered, changed := "buffer", 3
	if _, err := f.service.Update(t.Context(), v.Read.ID, reads.PatchParams{CatchUp: &buffered}); !errors.Is(err, reads.ErrFixedCatchUp) {
		t.Errorf("catch_up: %v", err)
	}
	if _, err := f.service.Update(t.Context(), v.Read.ID, reads.PatchParams{Buffer: &changed}); !errors.Is(err, reads.ErrFixedCatchUp) {
		t.Errorf("buffer: %v", err)
	}
	if _, err := f.service.Update(t.Context(), 9999, reads.PatchParams{Name: &name}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing read: %v", err)
	}
}

func TestDelete(t *testing.T) {
	f := newFixture(t, 1)
	v := f.create(t, daily())
	if err := f.service.Delete(t.Context(), v.Read.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(t.Context(), v.Read.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
	if err := f.service.Delete(t.Context(), v.Read.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second Delete = %v", err)
	}
	if _, err := f.service.Adjust(t.Context(), v.Read.ID, schedule.Pause{}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Adjust = %v", err)
	}
}

func TestList(t *testing.T) {
	f := newFixture(t, 3)
	f.create(t, daily())
	f.create(t, daily())
	views, err := f.service.List(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Errorf("views = %d", len(views))
	}
}

func TestConcurrentAdjustmentsSerialise(t *testing.T) {
	f := newFixture(t, 100)
	v := f.create(t, daily())

	const skips = 12
	var wg sync.WaitGroup
	errs := make([]error, skips)
	for i := range skips {
		wg.Go(func() {
			_, errs[i] = f.service.Adjust(context.Background(), v.Read.ID, schedule.Skip{Count: 1})
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}

	got, err := f.service.Get(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Position != skips {
		t.Errorf("position = %d, want %d: a skip was lost to another", got.Position, skips)
	}
	rows, err := f.store.Segments(t.Context(), v.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != skips+1 {
		t.Fatalf("segments = %d", len(rows))
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].FromSeq != rows[i-1].FromSeq+1 {
			t.Errorf("segment %d: from_seq %d after %d", i, rows[i].FromSeq, rows[i-1].FromSeq)
		}
	}
}

func TestFurthest(t *testing.T) {
	f := newFixture(t, 50)
	if _, ok, err := f.service.Furthest(t.Context(), f.source.ID); ok || err != nil {
		t.Errorf("no reads: %v, %v", ok, err)
	}
	first := f.create(t, daily())
	second := f.create(t, daily())
	f.adjust(t, second.Read.ID, schedule.Jump{Seq: 20})
	f.adjust(t, first.Read.ID, schedule.Jump{Seq: 5})
	got, ok, err := f.service.Furthest(t.Context(), f.source.ID)
	if err != nil || !ok {
		t.Fatalf("Furthest: %v, %v", ok, err)
	}
	if got != 20 {
		t.Errorf("Furthest = %d, want 20", got)
	}
}

func TestDiscoveryLookaheadFollowsReads(t *testing.T) {
	st := storetest.New(t)
	clock := sitetest.NewClock()
	site := sitetest.Serve(t, "comiccontrol")
	service := reads.New(reads.Options{Store: st, DefaultTimezone: "UTC", MinDatedShare: 0.9, Now: clock.Now})
	loop := discovery.NewLoop(discovery.LoopOptions{
		Store:        st,
		Registry:     discovery.NewRegistry(strategies.Crawl{}),
		Fetcher:      discovery.NewFetcher(sitetest.FetcherOptions(clock)),
		Metrics:      discovery.NewMetrics(prometheus.NewRegistry()),
		Logger:       slog.New(slog.DiscardHandler),
		Clock:        clock,
		Lookahead:    1,
		TailInterval: 6 * time.Hour,
		PollInterval: time.Minute,
		Furthest:     service.Furthest,
	})
	config, err := json.Marshal(map[string]string{"strategy": "crawl", "start_url": site.URL + "/1.html"})
	if err != nil {
		t.Fatal(err)
	}
	source, err := st.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: site.URL, DefaultKind: "page", Strategy: "crawl", Config: config, State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}
	settle := func() int64 {
		t.Helper()
		for range 20 {
			worked, err := loop.Tick(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				break
			}
		}
		count, _, err := st.EntryStats(t.Context(), source.ID)
		if err != nil {
			t.Fatal(err)
		}
		return count
	}

	if got := settle(); got != 1 {
		t.Fatalf("with no reads: %d entries, want the lookahead of 1", got)
	}

	read, err := service.Create(t.Context(), reads.CreateParams{SourceID: source.ID, Name: "r", Pacing: daily(), Initial: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := settle(); got != 2 {
		t.Fatalf("read at the start: %d entries, want 2", got)
	}

	// Two days on, the read is due seq 2, so the index goes to seq 3.
	clock.Advance(49 * time.Hour)
	if got := settle(); got != 4 {
		t.Fatalf("read moved on: %d entries, want 4 (read %d)", got, read.Read.ID)
	}
}
