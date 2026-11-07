// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
)

func (h *harness) sourceOf(t *testing.T, strategy, kind string, config map[string]any) db.Source {
	t.Helper()
	config["strategy"] = strategy
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s, err := h.store.CreateSource(t.Context(), db.InsertSourceParams{
		Slug: "s", Title: "t", HomeUrl: "https://home.example/", DefaultKind: kind, Strategy: strategy, Config: raw, State: "indexing",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func body(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

const newItem = "<item><title>Newest</title><guid>newest</guid><link>https://new.example/</link><pubDate>Mon, 01 Jan 2024 00:00:00 +0000</pubDate></item>"

// pagedFeed answers /feed.xml?paged=N from the fixture's numbered files, as
// WordPress does, and past the last page with a 404.
func pagedFeed(t *testing.T, site *sitetest.Site) *sync.Map {
	t.Helper()
	pages := &sync.Map{}
	for _, n := range []string{"1", "2", "3"} {
		pages.Store(n, body(t, site.URL+"/feed-"+n+".xml"))
	}
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/feed.xml" {
			return false
		}
		n := r.URL.Query().Get("paged")
		if n == "" {
			n = "1"
		}
		page, ok := pages.Load(n)
		if !ok {
			http.NotFound(w, r)
			return true
		}
		_, _ = io.WriteString(w, page.(string))
		return true
	})
	return pages
}

func TestEachStrategyIndexesThenTails(t *testing.T) {
	jsonFields := map[string]any{"title": "safe_title", "image": "img", "alt_text": "alt", "year": "year", "month": "month", "day": "day"}
	tests := []struct {
		name, site, strategy, kind string
		config                     func(url string) map[string]any
		want                       int
		first, last                string
		prepare                    func(t *testing.T, site *sitetest.Site) func()
	}{
		{
			name: "podcast feed", site: "podcast", strategy: "feed", kind: "episode", want: 5, first: "Episode 1", last: "Newest",
			config: func(url string) map[string]any { return map[string]any{"feed_url": url + "/feed.xml"} },
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				feed := body(t, site.URL+"/feed.xml")
				return func() { site.SetPage("/feed.xml", strings.Replace(feed, "<item>", newItem+"<item>", 1)) }
			},
		},
		{
			name: "archived feed", site: "archived-feed", strategy: "feed", kind: "article", want: 6, first: "Post 1", last: "Newest",
			config: func(url string) map[string]any { return map[string]any{"feed_url": url + "/feed.xml"} },
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				feed := body(t, site.URL+"/feed.xml")
				const entry = "<entry><id>newest</id><title>Newest</title><updated>2024-01-01T00:00:00Z</updated></entry>"
				return func() { site.SetPage("/feed.xml", strings.Replace(feed, "<entry>", entry+"<entry>", 1)) }
			},
		},
		{
			name: "paged feed", site: "news-paged", strategy: "feed-paged", kind: "article", want: 5, first: "Story 1", last: "Newest",
			config: func(url string) map[string]any { return map[string]any{"feed_url": url + "/feed.xml"} },
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				pages := pagedFeed(t, site)
				return func() {
					page, _ := pages.Load("1")
					pages.Store("1", strings.Replace(page.(string), "<item>", newItem+"<item>", 1))
				}
			},
		},
		{
			name: "numeric html with a gap", site: "numeric-html", strategy: "numeric", kind: "page", want: 4, first: "Number 1", last: "Number 6",
			config: func(url string) map[string]any {
				return map[string]any{"url_template": url + "/comic/{n}.html", "start": 1}
			},
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				return func() {
					site.SetPage("/comic/6.html", `<title>Number 6</title><div id="comic"><img src="/img/6.png"></div>`)
				}
			},
		},
		{
			name: "numeric json", site: "numeric-json", strategy: "numeric", kind: "page", want: 3, first: "Barrel", last: "Newest",
			config: func(url string) map[string]any {
				return map[string]any{"url_template": url + "/{n}/info.0.json", "start": 1, "format": "json", "fields": jsonFields}
			},
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				return func() {
					site.SetPage("/4/info.0.json", `{"safe_title":"Newest","img":"https://imgs.example/4.png","year":"2006","month":"1","day":"4"}`)
				}
			},
		},
		{
			name: "crawled blog", site: "wordpress-blog", strategy: "crawl", kind: "article", want: 4, first: "Post 1", last: "Newest",
			config: func(url string) map[string]any { return map[string]any{"start_url": url + "/1/"} },
			prepare: func(t *testing.T, site *sitetest.Site) func() {
				t.Helper()
				page := body(t, site.URL+"/4/")
				return func() {
					site.SetPage("/4/", strings.Replace(page, `<nav class="post-navigation">`, `<nav class="post-navigation"><div class="nav-next"><a href="/5/">Next</a></div>`, 1))
					site.SetPage("/5/", `<title>Newest</title><article><p>A new post.</p></article>`)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, 50)
			site := sitetest.Serve(t, tt.site)
			grow := tt.prepare(t, site)
			s := h.sourceOf(t, tt.strategy, tt.kind, tt.config(site.URL))

			h.settle(t)
			entries := h.entries(t, s.ID)
			if len(entries) != tt.want {
				t.Fatalf("entries = %d, want %d", len(entries), tt.want)
			}
			if got := *entries[0].Title; got != tt.first {
				t.Errorf("first entry = %q, want %q: the archive should run oldest first", got, tt.first)
			}
			if got := h.get(t, s.ID).State; got != "tail" {
				t.Fatalf("state = %q, want tail", got)
			}

			grow()
			h.clock.Advance(6*time.Hour + time.Minute)
			h.settle(t)
			entries = h.entries(t, s.ID)
			if len(entries) != tt.want+1 {
				t.Fatalf("after growth: entries = %d, want %d", len(entries), tt.want+1)
			}
			if got := *entries[tt.want].Title; got != tt.last {
				t.Errorf("new entry = %q, want %q", got, tt.last)
			}
			if got := h.get(t, s.ID).State; got != "tail" {
				t.Errorf("state = %q, want back in tail", got)
			}
		})
	}
}

func TestPodcastFeedKeepsItsMetadata(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "podcast")
	s := h.sourceOf(t, "feed", "episode", map[string]any{"feed_url": site.URL + "/feed.xml"})
	h.settle(t)

	got := h.get(t, s.ID)
	if got.ImageUrl == nil || *got.ImageUrl != "https://cdn.podcast.example/cover.jpg" {
		t.Errorf("artwork = %v", got.ImageUrl)
	}
	var extra map[string]any
	if err := json.Unmarshal(got.Extra, &extra); err != nil {
		t.Fatal(err)
	}
	if extra["itunes:author"] != "Some People" || extra["itunes:explicit"] != "false" {
		t.Errorf("source extra = %v", extra)
	}

	last := h.entries(t, s.ID)[4]
	if last.Guid == nil || *last.Guid != "episode-5-guid" {
		t.Errorf("guid = %v", last.Guid)
	}
	if last.Kind != "episode" {
		t.Errorf("kind = %q", last.Kind)
	}
	if want := int64(50 * time.Minute / time.Microsecond); last.Duration.Microseconds != want {
		t.Errorf("duration = %d, want %d", last.Duration.Microseconds, want)
	}
	var enclosures []struct {
		URL    string `json:"url"`
		Type   string `json:"type"`
		Length int64  `json:"length"`
	}
	if err := json.Unmarshal(last.Enclosures, &enclosures); err != nil {
		t.Fatal(err)
	}
	if len(enclosures) != 1 || enclosures[0].URL != "https://cdn.podcast.example/5.mp3" || enclosures[0].Length != 5000000 {
		t.Errorf("enclosures = %+v", enclosures)
	}
	var entryExtra map[string]any
	if err := json.Unmarshal(last.Extra, &entryExtra); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"itunes:image", "itunes:episode", "itunes:season", "podcast:transcript", "podcast:chapters"} {
		if entryExtra[key] == nil {
			t.Errorf("entry extra has no %s: %v", key, entryExtra)
		}
	}
}

// A save that fails leaves validators in crawl_log and no cursor. The retry starts
// the walk again, and a 304 there has no body, so it must not ask for one.
func TestRetryAfterAFailedSaveIsNotConditional(t *testing.T) {
	h := newHarness(t, 50)
	site := sitetest.Serve(t, "wordpress-webcomic")
	s := h.source(t, site.URL+"/comic/1/")
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 5 {
		t.Fatalf("entries = %d, want 5", got)
	}

	_, err := h.store.Pool().Exec(t.Context(), "DELETE FROM entries WHERE source_id = $1", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.store.Pool().Exec(t.Context(), "UPDATE sources SET cursor = NULL, state = 'indexing', due_at = NULL WHERE id = $1", s.ID)
	if err != nil {
		t.Fatal(err)
	}
	h.settle(t)
	if got := len(h.entries(t, s.ID)); got != 5 {
		t.Errorf("entries after the retry = %d, want 5", got)
	}
}
