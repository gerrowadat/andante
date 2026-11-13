// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type harness struct {
	store    *store.Store
	clock    *sitetest.Clock
	loop     *discovery.Loop
	metrics  *prometheus.Registry
	furthest atomic.Int32
	hasRead  atomic.Bool
}

func newHarness(t *testing.T, lookahead int) *harness {
	t.Helper()
	h := &harness{store: storetest.Catalogue(t), clock: sitetest.NewClock(), metrics: prometheus.NewRegistry()}
	h.loop = discovery.NewLoop(discovery.LoopOptions{
		Store:        h.store,
		Registry:     discovery.NewRegistry(strategies.All()...),
		Fetcher:      discovery.NewFetcher(sitetest.FetcherOptions(h.clock)),
		Metrics:      discovery.NewMetrics(h.metrics),
		Logger:       slog.New(slog.DiscardHandler),
		Clock:        h.clock,
		Lookahead:    lookahead,
		TailInterval: 6 * time.Hour,
		PollInterval: time.Minute,
		Furthest: func(context.Context, int64) (int32, bool, error) {
			return h.furthest.Load(), h.hasRead.Load(), nil
		},
	})
	return h
}

func (h *harness) source(t *testing.T, startURL string) db.Source {
	t.Helper()
	config, err := json.Marshal(map[string]string{"strategy": "crawl", "start_url": startURL})
	if err != nil {
		t.Fatal(err)
	}
	s, err := h.store.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: startURL, DefaultKind: "page", Strategy: "crawl", Config: config, State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (h *harness) tick(t *testing.T) bool {
	t.Helper()
	worked, err := h.loop.Tick(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return worked
}

// settle ticks until nothing is left to do.
func (h *harness) settle(t *testing.T) {
	t.Helper()
	for range 20 {
		if !h.tick(t) {
			return
		}
	}
	t.Fatal("the loop never settled")
}

func (h *harness) get(t *testing.T, id int64) db.Source {
	t.Helper()
	s, err := h.store.GetSource(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (h *harness) entries(t *testing.T, id int64) []db.Entry {
	t.Helper()
	entries, err := h.store.ListEntries(t.Context(), id, -1, 100)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// events returns a source's crawl log events, oldest first, as "level message".
func (h *harness) events(t *testing.T, id int64) []string {
	t.Helper()
	rows, err := h.store.ListCrawlLog(t.Context(), id, 0, "event", 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, row := range slices.Backward(rows) {
		out = append(out, *row.Level+" "+*row.Message)
	}
	return out
}

// logged reports whether a source's crawl log has an event that starts with prefix.
func (h *harness) logged(t *testing.T, id int64, prefix string) bool {
	t.Helper()
	return slices.ContainsFunc(h.events(t, id), func(e string) bool { return strings.HasPrefix(e, prefix) })
}

func TestIndexesToTheLiveEndThenTails(t *testing.T) {
	h := newHarness(t, 50)
	h.hasRead.Store(true)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")

	h.settle(t)

	entries := h.entries(t, s.ID)
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(entries))
	}
	for i, e := range entries {
		if int(e.Seq) != i {
			t.Errorf("entry %d has seq %d", i, e.Seq)
		}
		if e.Kind != "page" || e.Title == nil || len(e.Images) != 1 {
			t.Errorf("entry %d: %+v", i, e)
		}
	}
	got := h.get(t, s.ID)
	if got.State != "tail" {
		t.Errorf("state = %q, want tail", got.State)
	}
	if got.DueAt == nil || got.DueAt.Sub(h.clock.Now()) > 6*time.Hour || got.DueAt.Sub(h.clock.Now()) < 5*time.Hour {
		t.Errorf("due_at = %v, want about 6h on", got.DueAt)
	}
	if got.LastCrawledAt == nil || got.Cursor == nil {
		t.Errorf("crawled at %v, cursor %s", got.LastCrawledAt, got.Cursor)
	}
}

func TestSourceWithNoReadsIndexesOnlyTheLookahead(t *testing.T) {
	h := newHarness(t, 2)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")

	h.settle(t)

	if got := len(h.entries(t, s.ID)); got != 2 {
		t.Errorf("entries = %d, want the lookahead of 2", got)
	}
	if got := site.Count("/3.html"); got != 0 {
		t.Errorf("/3.html requested %d times past the lookahead", got)
	}
	if got := h.get(t, s.ID).State; got != "indexing" {
		t.Errorf("state = %q", got)
	}
}

func TestLookaheadFollowsReadsAndTailPicksUpANewPage(t *testing.T) {
	h := newHarness(t, 2)
	site := sitetest.Serve(t, "wordpress-webcomic")
	s := h.source(t, site.URL+"/comic/1/")
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 2 {
		t.Fatalf("entries = %d, want 2", got)
	}

	// A read at seq 0 keeps the index 2 ahead of it, so up to seq 2.
	h.hasRead.Store(true)
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 3 {
		t.Fatalf("entries = %d, want 3", got)
	}

	// The read moves on, so the index does.
	h.furthest.Store(2)
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 5 {
		t.Fatalf("entries = %d, want 5", got)
	}
	if got := h.get(t, s.ID).State; got != "tail" {
		t.Fatalf("state = %q, want tail at the live end", got)
	}

	// Nothing happens before the tail interval.
	if h.tick(t) {
		t.Error("a tail source was crawled before it was due")
	}

	// The site grows while the source is quiet.
	site.SetPage("/comic/5/", `<title>Strip 5</title><div class="webcomic-image"><img src="/5.png"></div><a rel="next" href="/comic/6/">n</a>`)
	site.SetPage("/comic/6/", `<title>Strip 6</title><div class="webcomic-image"><img src="/6.png"></div>`)
	h.clock.Advance(6*time.Hour + time.Minute)
	h.settle(t)

	entries := h.entries(t, s.ID)
	if len(entries) != 6 {
		t.Fatalf("entries = %d, want 6", len(entries))
	}
	if last := entries[5]; last.Seq != 5 || *last.Title != "Strip 6" {
		t.Errorf("new entry = seq %d, %v", last.Seq, last.Title)
	}
	if got := h.get(t, s.ID).State; got != "tail" {
		t.Errorf("state = %q, want back in tail", got)
	}
}

func TestSeqNeverChangesWhenAPageIsUpdated(t *testing.T) {
	h := newHarness(t, 50)
	h.hasRead.Store(true)
	site := sitetest.Serve(t, "wordpress-webcomic")
	s := h.source(t, site.URL+"/comic/1/")
	h.settle(t)
	before := h.entries(t, s.ID)

	site.SetPage("/comic/5/", `<meta property="og:title" content="Strip 5, recoloured"><div class="webcomic-image"><img src="/new.png"></div>`)
	h.clock.Advance(7 * time.Hour)
	h.settle(t)

	after := h.entries(t, s.ID)
	if len(after) != len(before) {
		t.Fatalf("entries = %d, want %d", len(after), len(before))
	}
	last := after[4]
	if last.Seq != before[4].Seq || last.ID != before[4].ID {
		t.Errorf("the entry moved: seq %d, id %d", last.Seq, last.ID)
	}
	if *last.Title != "Strip 5, recoloured" {
		t.Errorf("title = %q, want it updated in place", *last.Title)
	}
}

func TestRequestedCrawlForcesAPass(t *testing.T) {
	h := newHarness(t, 1)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 1 {
		t.Fatalf("entries = %d, want 1", got)
	}

	if err := h.store.RequestCrawl(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Second)
	if !h.tick(t) {
		t.Fatal("a requested crawl did nothing")
	}
	if got := len(h.entries(t, s.ID)); got != 5 {
		t.Errorf("entries = %d, want the rest of the batch", got)
	}
}

func TestStoppingConditions(t *testing.T) {
	t.Run("robots disallow", func(t *testing.T) {
		h := newHarness(t, 50)
		site := sitetest.Serve(t, "robots-disallow")
		s := h.source(t, site.URL+"/comics/1.html")
		h.settle(t)
		got := h.get(t, s.ID)
		if got.State != "error" || got.LastError == nil {
			t.Fatalf("state = %q, error %v", got.State, got.LastError)
		}
		if !h.logged(t, s.ID, "error stopped: ") {
			t.Errorf("events = %q", h.events(t, s.ID))
		}
		if site.Count("/comics/1.html") != 0 {
			t.Error("a disallowed page was requested")
		}
	})
	t.Run("cycle", func(t *testing.T) {
		h := newHarness(t, 50)
		site := sitetest.Serve(t, "cycle")
		s := h.source(t, site.URL+"/1.html")
		h.settle(t)
		got := h.get(t, s.ID)
		if got.State != "error" || got.LastError == nil {
			t.Fatalf("state = %q, error %v", got.State, got.LastError)
		}
	})
	t.Run("cycle across batches", func(t *testing.T) {
		h := newHarness(t, 2)
		site := sitetest.Serve(t, "cycle")
		s := h.source(t, site.URL+"/1.html")
		h.settle(t)
		if got := h.get(t, s.ID).State; got != "indexing" {
			t.Fatalf("state = %q", got)
		}
		h.hasRead.Store(true)
		h.furthest.Store(50)
		h.settle(t)
		got := h.get(t, s.ID)
		if got.State != "error" {
			t.Fatalf("state = %q, want error", got.State)
		}
		if got := len(h.entries(t, s.ID)); got > 3 {
			t.Errorf("entries = %d: the cycle was indexed", got)
		}
	})
	t.Run("backoff waits and recovers", func(t *testing.T) {
		h := newHarness(t, 50)
		site := sitetest.Serve(t, "rate-limited")
		var limited atomic.Bool
		limited.Store(true)
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/1.html" || !limited.Load() {
				return false
			}
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		})
		s := h.source(t, site.URL+"/1.html")

		h.settle(t)
		got := h.get(t, s.ID)
		if got.State != "indexing" {
			t.Errorf("state = %q, want it unchanged", got.State)
		}
		if got.DueAt == nil || got.DueAt.Before(h.clock.Now().Add(50*time.Minute)) {
			t.Errorf("due_at = %v, want about an hour on", got.DueAt)
		}
		if got.LastError == nil {
			t.Error("last_error is empty")
		}
		if !h.logged(t, s.ID, "warn waiting until ") {
			t.Errorf("events = %q", h.events(t, s.ID))
		}

		limited.Store(false)
		h.clock.Advance(61 * time.Minute)
		h.settle(t)
		got = h.get(t, s.ID)
		if got.State != "tail" || got.LastError != nil {
			t.Errorf("after recovery: state %q, error %v", got.State, got.LastError)
		}
		if n := len(h.entries(t, s.ID)); n != 5 {
			t.Errorf("entries = %d", n)
		}
	})
	t.Run("unknown strategy", func(t *testing.T) {
		h := newHarness(t, 50)
		s, err := h.store.CreateSource(t.Context(), db.InsertSourceParams{
			Slug: "f", Title: "t", HomeUrl: "https://feed.example/", DefaultKind: "episode",
			Strategy: "feed", Config: []byte(`{"feed_url":"https://feed.example/rss"}`), State: "indexing",
		})
		if err != nil {
			t.Fatal(err)
		}
		h.settle(t)
		if got := h.get(t, s.ID).State; got != "error" {
			t.Errorf("state = %q", got)
		}
	})
}

func TestPausedSourcesAreLeftAlone(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	_, err := h.store.UpdateSource(t.Context(), s.ID, func(p *db.UpdateSourceParams) error {
		p.State = "paused"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.tick(t) {
		t.Error("a paused source was crawled")
	}
	if got := len(site.Requests()); got != 0 {
		t.Errorf("site got %d requests", got)
	}
}

func TestSourcePausedDuringACrawlStaysPaused(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	site.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/2.html" {
			_, err := h.store.UpdateSource(context.Background(), s.ID, func(p *db.UpdateSourceParams) error {
				p.State = "paused"
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}
		return false
	})
	h.tick(t)
	if got := h.get(t, s.ID).State; got != "paused" {
		t.Errorf("state = %q, want paused", got)
	}
}

func TestCrawlLogAndConditionalRequests(t *testing.T) {
	h := newHarness(t, 50)
	h.hasRead.Store(true)
	site := sitetest.Serve(t, "wordpress-webcomic")
	s := h.source(t, site.URL+"/comic/1/")
	h.settle(t)

	var logged int
	err := h.store.Pool().QueryRow(t.Context(), "SELECT count(*) FROM crawl_log WHERE source_id = $1 AND status = 200", s.ID).Scan(&logged)
	if err != nil {
		t.Fatal(err)
	}
	if logged != 5 {
		t.Errorf("crawl_log rows = %d, want 5", logged)
	}

	var conditional atomic.Int32
	site.Intercept(func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("If-Modified-Since") != "" {
			conditional.Add(1)
		}
		return false
	})
	h.clock.Advance(7 * time.Hour)
	h.settle(t)
	if conditional.Load() == 0 {
		t.Error("the tail check was not conditional")
	}
}

func TestCrawlLogSaysWhy(t *testing.T) {
	h := newHarness(t, 50)
	h.hasRead.Store(true)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	h.settle(t)

	want := []string{
		"info looking for up to 10 entries: the furthest read is at 0 and it keeps 50 entries ahead, but has up to -1",
		"info starting at " + site.URL + "/1.html",
		"warn next link points to the current page on " + site.URL + "/5.html",
		"info the next link on " + site.URL + "/5.html points back at it, so this is the live end",
		"info indexed 5 entries; at the live end, checking again at " + h.clock.Now().Add(6*time.Hour).UTC().Format(time.RFC3339),
	}
	if got := h.events(t, s.ID); !slices.Equal(got, want) {
		t.Errorf("events = %q\nwant %q", got, want)
	}
	rows, err := h.store.ListCrawlLog(t.Context(), s.ID, 0, "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	var kept []int64
	for _, row := range rows {
		if row.HasPage {
			kept = append(kept, row.ID)
		}
	}
	if len(kept) != 1 {
		t.Fatalf("kept pages = %v, want one", kept)
	}
	page, err := h.store.CrawledPage(t.Context(), s.ID, kept[0])
	if err != nil {
		t.Fatal(err)
	}
	if *page.Url != site.URL+"/5.html" || !strings.Contains(string(page.Body), "<html") {
		t.Errorf("kept page = %s: %.80s", *page.Url, page.Body)
	}

	h.clock.Advance(7 * time.Hour)
	h.settle(t)
	events := h.events(t, s.ID)
	if got := events[len(want):]; len(got) < 2 || got[0] != "info looking for up to 10 entries: checking the live end for new entries" || got[1] != "info "+site.URL+"/5.html has not changed since the last check" ||
		!strings.HasPrefix(got[2], "info found nothing new; at the live end") {
		t.Errorf("tail check events = %q", got)
	}
}

func TestCrawlLogKeepsThePageWithNoNextLink(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "missing-next")
	s := h.source(t, site.URL+"/1.html")
	h.settle(t)

	rows, err := h.store.ListCrawlLog(t.Context(), s.ID, 0, "event", 1000)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rows, func(r db.ListCrawlLogRow) bool { return r.HasPage })
	if i < 0 {
		t.Fatalf("no page kept: %q", h.events(t, s.ID))
	}
	if got := *rows[i].Level + " " + *rows[i].Message; !strings.HasPrefix(got, "warn no next link on ") {
		t.Errorf("event = %q", got)
	}
	page, err := h.store.CrawledPage(t.Context(), s.ID, rows[i].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page.Body), "Gap 3") {
		t.Errorf("kept page = %.200s", page.Body)
	}
}

func TestMetrics(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "comiccontrol")
	h.source(t, site.URL+"/1.html")
	h.settle(t)

	for _, name := range []string{
		"andante_crawl_requests_total", "andante_crawl_duration_seconds",
		"andante_entries_discovered_total", "andante_source_state",
	} {
		count, err := testutil.GatherAndCount(h.metrics, name)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Errorf("%s has no series", name)
		}
	}
	families, err := h.metrics.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range families {
		if f.GetName() != "andante_entries_discovered_total" {
			continue
		}
		if got := f.GetMetric()[0].GetCounter().GetValue(); got != 5 {
			t.Errorf("entries discovered = %v, want 5", got)
		}
	}
}

func TestOnlyOneReplicaCrawls(t *testing.T) {
	st := storetest.Catalogue(t)
	first, err := st.TryCrawlLock(t.Context())
	if err != nil || first == nil {
		t.Fatalf("first lock: %v, %v", first, err)
	}
	second, err := st.TryCrawlLock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second != nil {
		t.Error("a second replica took the lock")
	}
	if !first.Held(t.Context()) {
		t.Error("Held = false for a live lock")
	}
	first.Release(t.Context())
	third, err := st.TryCrawlLock(t.Context())
	if err != nil || third == nil {
		t.Fatalf("lock after release: %v, %v", third, err)
	}
	third.Release(t.Context())
}

func TestReleasingALockFreesItAtOnce(t *testing.T) {
	st := storetest.Catalogue(t)
	for i := range 50 {
		lock, err := st.TryCrawlLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if lock == nil {
			t.Fatalf("round %d: the lock was still held after its release", i)
		}
		lock.Release(t.Context())
	}
}

func TestReleaseWorksAfterTheContextEnds(t *testing.T) {
	st := storetest.Catalogue(t)
	lock, err := st.TryCrawlLock(t.Context())
	if err != nil || lock == nil {
		t.Fatalf("lock: %v, %v", lock, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	lock.Release(ctx)

	// The unlock failed, so the fallback closed the session, which takes a moment.
	deadline := time.Now().Add(5 * time.Second)
	for {
		again, err := st.TryCrawlLock(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if again != nil {
			again.Release(t.Context())
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lock was never freed")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	h := newHarness(t, 50)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- h.loop.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// Nobody reads it, so at the live end it stops being checked, until somebody starts.
func TestAnUnreadSourceParksAtTheLiveEnd(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	h.settle(t)

	got := h.get(t, s.ID)
	if got.State != "tail" || got.DueAt != nil {
		t.Fatalf("state = %q, due_at = %v, want parked in tail", got.State, got.DueAt)
	}
	requests := len(site.Requests())
	h.clock.Advance(7 * 24 * time.Hour)
	if h.tick(t) {
		t.Error("a parked source was crawled")
	}
	if got := len(site.Requests()); got != requests {
		t.Errorf("requests went from %d to %d while parked", requests, got)
	}

	// Someone starts a read, and the catalogue hears about it.
	seq := int32(0)
	if err := h.store.SetDemand(t.Context(), s.ID, &seq); err != nil {
		t.Fatal(err)
	}
	if got := h.get(t, s.ID); got.DueAt == nil {
		t.Fatal("demand didn't wake the source")
	}
	h.hasRead.Store(true)
	site.SetPage("/5.html", `<title>Harbour Lights - Page 5</title><div id="cc-comicbody"><img src="/5.png"></div><a rel="next" href="/6.html">n</a>`)
	site.SetPage("/6.html", `<title>Harbour Lights - Page 6</title><div id="cc-comicbody"><img src="/6.png"></div>`)
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 6 {
		t.Errorf("entries after waking = %d, want 6", got)
	}
	if got := h.get(t, s.ID); got.State != "tail" || got.DueAt == nil {
		t.Errorf("state = %q, due_at = %v, want tail with a next check", got.State, got.DueAt)
	}

	// Demand that only moves doesn't book an extra check.
	due := h.get(t, s.ID).DueAt
	seq = 3
	if err := h.store.SetDemand(t.Context(), s.ID, &seq); err != nil {
		t.Fatal(err)
	}
	if got := h.get(t, s.ID).DueAt; !got.Equal(*due) {
		t.Errorf("due_at moved from %v to %v", due, got)
	}
}

func TestARequestedCrawlStillChecksAParkedSource(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "comiccontrol")
	s := h.source(t, site.URL+"/1.html")
	h.settle(t)
	if err := h.store.RequestCrawl(t.Context(), s.ID); err != nil {
		t.Fatal(err)
	}
	before := len(site.Requests())
	h.settle(t)
	if len(site.Requests()) == before {
		t.Error("the requested crawl didn't fetch anything")
	}
	if got := h.get(t, s.ID); got.State != "tail" || got.DueAt != nil {
		t.Errorf("state = %q, due_at = %v, want parked again", got.State, got.DueAt)
	}
}
