// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package find_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/find"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fixture struct {
	store    *store.Store
	finder   *find.Finder
	clock    *sitetest.Clock
	registry *prometheus.Registry
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: storetest.Catalogue(t), clock: sitetest.NewClock(), registry: prometheus.NewRegistry()}
	metrics := discovery.NewMetrics(f.registry)
	fetcher := discovery.NewFetcher(sitetest.FetcherOptions(f.clock))
	prober := discovery.NewProber(discovery.NewRegistry(strategies.All()...), fetcher, metrics)
	f.finder = find.New(f.store, prober, metrics)
	return f
}

func (f *fixture) find(t *testing.T, address string) []find.Candidate {
	t.Helper()
	candidates, err := f.finder.Find(t.Context(), address, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	return candidates
}

func (f *fixture) add(t *testing.T, slug, title, home, strategy, config string) db.Source {
	t.Helper()
	source, err := f.store.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: slug, Title: title, HomeUrl: home, DefaultKind: "page", Strategy: strategy, Config: []byte(config), State: "tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func strategyOf(t *testing.T, c find.Candidate) string {
	t.Helper()
	var config struct {
		Strategy string `json:"strategy"`
	}
	if err := json.Unmarshal(c.Config, &config); err != nil {
		t.Fatal(err)
	}
	return config.Strategy
}

// paged answers /feed.xml?paged=N from the fixture's numbered files, as WordPress does.
func paged(t *testing.T, site *sitetest.Site) {
	t.Helper()
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/feed.xml" {
			return false
		}
		page := r.URL.Query().Get("paged")
		if page == "" {
			page = "1"
		}
		body, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sites", "discover-wordpress", "feed-"+page+".xml"))
		if err != nil {
			http.NotFound(w, r)
			return true
		}
		_, _ = w.Write(body)
		return true
	})
}

func TestAFeedWithTheWholeArchive(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if strategyOf(t, c) != "feed" || c.Title != "A Full Blog" || c.Kind != "article" || c.HomeURL != "https://full.example/" {
		t.Errorf("candidate = %+v", c)
	}
	if len(c.Sample) != 3 || c.Sample[0].Title != "A Full Blog 1" || c.DatedShare != 1 {
		t.Errorf("sample = %+v, dated %v", c.Sample, c.DatedShare)
	}
	if c.ExistingSourceID != nil || len(c.SimilarSourceIDs) != 0 {
		t.Errorf("a new site matched something: %+v", c)
	}
}

func TestAPodcast(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "podcast")
	candidates := f.find(t, site.URL+"/feed.xml")
	if len(candidates) != 1 || candidates[0].Kind != "episode" || strategyOf(t, candidates[0]) != "feed" {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestAFeedThatsCutOff(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-cutoff")
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 || strategyOf(t, candidates[0]) != "feed" {
		t.Fatalf("candidates = %+v", candidates)
	}
	warnings := strings.Join(candidates[0].Warnings, " ")
	if !strings.Contains(warnings, "may hold only the latest") {
		t.Errorf("warnings = %q", warnings)
	}
}

func TestAWordPressSite(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-wordpress")
	paged(t, site)
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 || strategyOf(t, candidates[0]) != "feed-paged" {
		t.Fatalf("candidates = %+v", candidates)
	}
	// The probe stops early, but a source made from this walks every page.
	if strings.Contains(string(candidates[0].Config), "max_pages") {
		t.Errorf("config = %s", candidates[0].Config)
	}
}

func TestAComicWithNoFeed(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-comic")
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if strategyOf(t, c) != "crawl" || !strings.Contains(string(c.Config), "/1.html") {
		t.Errorf("config = %s", c.Config)
	}
	if c.Title != "The Comic" || c.Kind != "page" || len(c.Sample) != 3 {
		t.Errorf("candidate = %+v", c)
	}
	if got := site.Count("/feed"); got != 1 {
		t.Errorf("looked for /feed %d times", got)
	}
}

func TestAComicWithOnlyButtons(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "labelled-nav")
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if strategyOf(t, c) != "crawl" || !strings.Contains(string(c.Config), "/1.html") {
		t.Errorf("config = %s", c.Config)
	}
	if len(c.Sample) != 3 {
		t.Fatalf("sample = %+v", c.Sample)
	}
	for i, e := range c.Sample {
		if want := fmt.Sprintf("%s/comics/%d.jpg?v=1", site.URL, i+1); len(e.Images) != 1 || e.Images[0] != want {
			t.Errorf("entry %d images = %v, want %s", i+1, e.Images, want)
		}
	}
}

func TestACrawlComesBeforeAFeedThatsCutOff(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-comic")
	feed, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sites", "discover-cutoff", "feed.xml"))
	if err != nil {
		t.Fatal(err)
	}
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/feed" {
			return false
		}
		_, _ = w.Write(feed)
		return true
	})
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 2 || strategyOf(t, candidates[0]) != "crawl" || strategyOf(t, candidates[1]) != "feed" {
		t.Fatalf("candidates = %+v", candidates)
	}
}

func TestNothingToGoOn(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	site.SetPage("/", "<title>Nothing here</title>")
	site.SetPage("/index.html", "<title>Nothing here</title>")
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/":
			_, _ = io.WriteString(w, "<title>Nothing here</title>")
			return true
		case "/feed.xml":
			http.NotFound(w, r)
			return true
		}
		return false
	})
	if candidates := f.find(t, site.URL+"/"); len(candidates) != 0 {
		t.Errorf("candidates = %+v", candidates)
	}
	if got := testutil.ToFloat64(metric(t, f, "none")); got != 1 {
		t.Errorf("metric = %v", got)
	}
}

func metric(t *testing.T, f *fixture, result string) prometheus.Collector {
	t.Helper()
	return f.counter(t, "andante_discoveries_total", result)
}

// counter is the value of one label of a counter with one label.
func (f *fixture) counter(t *testing.T, name, label string) prometheus.Collector {
	t.Helper()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "x"})
	families, err := f.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			if m.GetLabel()[0].GetValue() == label {
				counter.Add(m.GetCounter().GetValue())
			}
		}
	}
	return counter
}

func TestAddingASiteTwiceAsksNothingOfTheSite(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	first := f.find(t, site.URL+"/")
	if len(first) != 1 {
		t.Fatalf("candidates = %+v", first)
	}
	source, err := f.store.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: "blog", Title: first[0].Title, HomeUrl: site.URL + "/", DefaultKind: first[0].Kind,
		Strategy: "feed", Config: first[0].Config, State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}

	before := len(site.Requests())
	for _, address := range []string{site.URL, site.URL + "/", site.URL + "/#top", site.URL + "/feed.xml"} {
		again := f.find(t, address)
		if len(again) != 1 || again[0].ExistingSourceID == nil || *again[0].ExistingSourceID != source.ID {
			t.Errorf("%s: candidates = %+v", address, again)
		}
	}
	if got := len(site.Requests()); got != before {
		t.Errorf("made %d more requests: %v", got-before, site.Requests()[before:])
	}
	if got := testutil.ToFloat64(metric(t, f, "existing")); got != 4 {
		t.Errorf("metric = %v", got)
	}
}

func TestAFeedKnownByAnotherAddress(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	source := f.add(t, "blog", "Another Name", "https://elsewhere.example/", "feed", `{"feed_url":"`+site.URL+`/feed.xml"}`)
	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 || candidates[0].ExistingSourceID == nil || *candidates[0].ExistingSourceID != source.ID {
		t.Fatalf("candidates = %+v", candidates)
	}
	if len(candidates[0].Sample) != 0 {
		t.Errorf("probed something already here: %+v", candidates[0].Sample)
	}
}

func TestSomethingThatLooksClose(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	byTitle := f.add(t, "title", "a full BLOG", "https://other.example/", "crawl", `{"start_url":"https://other.example/1"}`)
	byHome := f.add(t, "home", "Different", "https://full.example", "crawl", `{"start_url":"https://full.example/1"}`)
	f.add(t, "neither", "Unrelated", "https://third.example/", "crawl", `{"start_url":"https://third.example/1"}`)

	candidates := f.find(t, site.URL+"/")
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v", candidates)
	}
	c := candidates[0]
	if c.ExistingSourceID != nil {
		t.Errorf("a close match was called certain: %+v", c)
	}
	if len(c.SimilarSourceIDs) != 2 || c.SimilarSourceIDs[0] != byTitle.ID || c.SimilarSourceIDs[1] != byHome.ID {
		t.Errorf("similar = %v", c.SimilarSourceIDs)
	}
}

func TestDiscoveryKeepsToRobotsAndTheInterval(t *testing.T) {
	f := newFixture(t)
	blocked := sitetest.Serve(t, "robots-disallow")
	if _, err := f.finder.Find(t.Context(), blocked.URL+"/comics/1.html", 3, nil); !errors.Is(err, discovery.ErrDisallowed) {
		t.Errorf("a disallowed site = %v", err)
	}
	if got := blocked.Count("/comics/1.html"); got != 0 {
		t.Errorf("fetched a disallowed page %d times", got)
	}

	site := sitetest.Serve(t, "discover-comic")
	f.find(t, site.URL+"/")
	requests := len(site.Requests())
	if requests < 2 {
		t.Fatalf("requests = %v", site.Requests())
	}
	sleeps := f.clock.Sleeps()
	if len(sleeps) < requests-1 {
		t.Errorf("%d waits for %d requests", len(sleeps), requests)
	}
	for _, wait := range sleeps {
		if wait <= 0 || wait > sitetest.FetcherOptions(f.clock).Interval {
			t.Errorf("wait = %s", wait)
		}
	}
}

func TestASiteThatWontAnswer(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/robots.txt" {
			return false
		}
		http.Error(w, "no", http.StatusForbidden)
		return true
	})
	if _, err := f.finder.Find(t.Context(), site.URL+"/", 3, nil); err == nil {
		t.Error("Find succeeded")
	}
	if got := testutil.ToFloat64(metric(t, f, "failed")); got != 1 {
		t.Errorf("metric = %v", got)
	}
}

// stepsOf finds candidates for an address and returns the steps it reported.
func (f *fixture) stepsOf(t *testing.T, address string) []find.Stage {
	t.Helper()
	var stages []find.Stage
	if _, err := f.finder.Find(t.Context(), address, 3, func(s find.Stage) { stages = append(stages, s) }); err != nil {
		t.Fatal(err)
	}
	return stages
}

func steps(stages []find.Stage) string {
	var b strings.Builder
	for _, s := range stages {
		b.WriteString(s.Step + " ")
	}
	return strings.TrimSpace(b.String())
}

func TestStagesOfAPagedFeed(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-wordpress")
	paged(t, site)
	stages := f.stepsOf(t, site.URL+"/")
	if got, want := steps(stages), "fetch feed paged probe"; got != want {
		t.Fatalf("steps = %q, want %q (%+v)", got, want, stages)
	}
	if got := stages[0].Message; got != "Fetching "+site.URL+"/" {
		t.Errorf("first message = %q", got)
	}
	if got := stages[3].Message; got != "Trying 1 of 1: its feed, page by page" {
		t.Errorf("last message = %q", got)
	}
	if got := testutil.ToFloat64(f.counter(t, "andante_discovery_stages_total", "probe")); got != 1 {
		t.Errorf("probe stages counted = %v", got)
	}
}

func TestStagesOfAComicWithNoFeed(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-comic")
	stages := f.stepsOf(t, site.URL+"/")
	if got, want := steps(stages), "fetch feeds feeds feeds feeds feeds probe"; got != want {
		t.Fatalf("steps = %q, want %q", got, want)
	}
	if got := stages[1].Message; got != "No feed is linked, so trying "+site.URL+"/feed" {
		t.Errorf("message = %q", got)
	}
}

func TestStagesWithoutAListenerAreStillCounted(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "discover-full")
	f.find(t, site.URL+"/")
	if got := testutil.ToFloat64(f.counter(t, "andante_discovery_stages_total", "fetch")); got != 1 {
		t.Errorf("fetch stages counted = %v", got)
	}
}
