// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/prometheus/client_golang/prometheus"
)

func raw(t *testing.T, config map[string]any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func probeWith(t *testing.T, strategy, kind string, config map[string]any, limit int) (discovery.ProbeResult, error) {
	t.Helper()
	config["strategy"] = strategy
	prober := discovery.NewProber(discovery.NewRegistry(strategies.All()...),
		discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock())), discovery.NewMetrics(prometheus.NewRegistry()))
	return prober.Probe(t.Context(), discovery.ProbeRequest{Strategy: strategy, Config: raw(t, config), Kind: kind, Limit: limit})
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		strategy discovery.Strategy
		config   string
		valid    bool
	}{
		{"feed", strategies.Feed{}, `{"strategy":"feed","feed_url":"https://a.example/rss"}`, true},
		{"feed, bad scheme", strategies.Feed{}, `{"strategy":"feed","feed_url":"ftp://a.example/rss"}`, false},
		{"feed, no URL", strategies.Feed{}, `{"strategy":"feed"}`, false},
		{"feed, unknown field", strategies.Feed{}, `{"strategy":"feed","feed_url":"https://a.example/rss","guess":1}`, false},
		{"feed-paged", strategies.FeedPaged{}, `{"strategy":"feed-paged","feed_url":"https://a.example/rss","max_pages":3}`, true},
		{"numeric", strategies.Numeric{}, `{"strategy":"numeric","url_template":"https://a.example/{n}","start":1}`, true},
		{"numeric, no placeholder", strategies.Numeric{}, `{"strategy":"numeric","url_template":"https://a.example/1","start":1}`, false},
		{"numeric, bad format", strategies.Numeric{}, `{"strategy":"numeric","url_template":"https://a.example/{n}","start":1,"format":"xml"}`, false},
		{"numeric, json without fields", strategies.Numeric{}, `{"strategy":"numeric","url_template":"https://a.example/{n}","start":1,"format":"json"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.strategy.Validate(json.RawMessage(tt.config))
			if (err == nil) != tt.valid {
				t.Errorf("Validate = %v, want valid = %v", err, tt.valid)
			}
		})
	}
}

func TestFeedProbes(t *testing.T) {
	t.Run("items sharing a link are not a cycle", func(t *testing.T) {
		site := sitetest.Serve(t, "podcast")
		result, err := probeWith(t, "feed", "episode", map[string]any{"feed_url": site.URL + "/feed.xml"}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(result.Entries); !slices.Equal(got, []string{"Episode 1", "Episode 2"}) {
			t.Errorf("titles = %v", got)
		}
	})
	t.Run("archive chain", func(t *testing.T) {
		site := sitetest.Serve(t, "archived-feed")
		result, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 6 || result.Entries[0].Title != "Post 1" {
			t.Errorf("titles = %v", titles(result.Entries))
		}
	})
	t.Run("archive loop", func(t *testing.T) {
		site := sitetest.Serve(t, "archived-feed")
		site.SetPage("/archive-1.xml", `<feed xmlns="http://www.w3.org/2005/Atom"><link rel="prev-archive" href="feed.xml"/><entry><id>x</id><title>x</title></entry></feed>`)
		if _, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25); err == nil || !strings.Contains(err.Error(), "cycle") {
			t.Errorf("probe = %v, want a cycle", err)
		}
	})
	t.Run("not a feed", func(t *testing.T) {
		site := sitetest.Serve(t, "podcast")
		site.SetPage("/feed.xml", "<html>no</html>")
		if _, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25); err == nil {
			t.Error("probe accepted a page that is not a feed")
		}
	})
	t.Run("a short feed is not called truncated", func(t *testing.T) {
		site := sitetest.Serve(t, "blog-summary")
		result, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Warnings) != 0 {
			t.Errorf("warnings = %v", result.Warnings)
		}
	})
	t.Run("a round number of items looks truncated", func(t *testing.T) {
		site := sitetest.Serve(t, "podcast")
		var items strings.Builder
		for i := range 10 {
			items.WriteString("<item><title>t</title><guid>g" + string(rune('a'+i)) + "</guid></item>")
		}
		site.SetPage("/feed.xml", `<rss version="2.0"><channel><title>c</title>`+items.String()+`</channel></rss>`)
		result, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Warnings) == 0 {
			t.Error("no warning for a feed cut off at 10 items")
		}
	})
}

func TestFeedContentIsSanitised(t *testing.T) {
	site := sitetest.Serve(t, "blog-summary")
	result, err := probeWith(t, "feed", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25)
	if err != nil {
		t.Fatal(err)
	}
	content := result.Entries[1].ContentHTML
	if !strings.Contains(content, "Kept text") {
		t.Errorf("content = %q", content)
	}
	for _, bad := range []string{"<script", "onclick", "<iframe", "steal"} {
		if strings.Contains(content, bad) {
			t.Errorf("content kept %s: %q", bad, content)
		}
	}
}

// localFeed points the summary feed's links at the fixture site, and gives each
// item a page.
func localFeed(t *testing.T) *sitetest.Site {
	t.Helper()
	site := sitetest.Serve(t, "blog-summary")
	feed := `<rss version="2.0"><channel><title>c</title>
<item><title>Two</title><link>` + site.URL + `/2</link><description>two</description></item>
<item><title>One</title><link>` + site.URL + `/1</link><description>one</description></item>
</channel></rss>`
	site.SetPage("/feed.xml", feed)
	long := strings.Repeat("Enough text to count as the main part of an article. ", 20)
	for _, n := range []string{"1", "2"} {
		site.SetPage("/"+n, `<title>p</title><meta property="og:image" content="/`+n+`.png"><article><p>`+long+`</p></article>`)
	}
	return site
}

func TestFillGaps(t *testing.T) {
	config := func(site *sitetest.Site, extra map[string]any) map[string]any {
		config := map[string]any{"feed_url": site.URL + "/feed.xml"}
		for k, v := range extra {
			config[k] = v
		}
		return config
	}
	t.Run("off", func(t *testing.T) {
		site := localFeed(t)
		result, err := probeWith(t, "feed", "article", config(site, nil), 25)
		if err != nil {
			t.Fatal(err)
		}
		if site.Count("/1") != 0 || len(result.Entries[0].Images) != 0 {
			t.Errorf("fetched item pages without fill_gaps: %v", site.Requests())
		}
	})
	t.Run("on, without full text", func(t *testing.T) {
		site := localFeed(t)
		result, err := probeWith(t, "feed", "article", config(site, map[string]any{"fill_gaps": true}), 25)
		if err != nil {
			t.Fatal(err)
		}
		e := result.Entries[0]
		if len(e.Images) != 1 || e.ContentHTML != "" {
			t.Errorf("images = %v, content = %q", e.Images, e.ContentHTML)
		}
	})
	t.Run("on, with full text", func(t *testing.T) {
		site := localFeed(t)
		result, err := probeWith(t, "feed", "article", config(site, map[string]any{"fill_gaps": true, "full_text": true}), 25)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(result.Entries[0].ContentHTML, "main part of an article") {
			t.Errorf("content = %q", result.Entries[0].ContentHTML)
		}
	})
	t.Run("keeps to the limit", func(t *testing.T) {
		site := localFeed(t)
		if _, err := probeWith(t, "feed", "article", config(site, map[string]any{"fill_gaps": true}), 1); err != nil {
			t.Fatal(err)
		}
		if site.Count("/2") != 0 {
			t.Errorf("fetched past the limit: %v", site.Requests())
		}
	})
	t.Run("a page that fails leaves a warning", func(t *testing.T) {
		site := localFeed(t)
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/1" {
				http.Error(w, "gone", http.StatusGone)
				return true
			}
			return false
		})
		result, err := probeWith(t, "feed", "article", config(site, map[string]any{"fill_gaps": true}), 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 2 || !warned(result.Warnings, "could not fill") {
			t.Errorf("entries = %d, warnings = %v", len(result.Entries), result.Warnings)
		}
	})
}

func TestFeedPagedStops(t *testing.T) {
	serve := func(t *testing.T, feed func(page string) (string, int)) *sitetest.Site {
		t.Helper()
		site := sitetest.Serve(t, "news-paged")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			body, status := feed(r.URL.Query().Get("paged"))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
			return true
		})
		return site
	}
	one := func(guid string) string {
		return `<rss version="2.0"><channel><title>c</title><item><title>` + guid + `</title><guid>` + guid + `</guid></item></channel></rss>`
	}
	t.Run("a site that ignores the parameter", func(t *testing.T) {
		site := serve(t, func(string) (string, int) { return one("same"), http.StatusOK })
		result, err := probeWith(t, "feed-paged", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 1 || site.Count("/feed.xml") != 2 {
			t.Errorf("entries = %d, requests = %v", len(result.Entries), site.Requests())
		}
	})
	t.Run("max pages", func(t *testing.T) {
		site := serve(t, func(page string) (string, int) { return one(page), http.StatusOK })
		result, err := probeWith(t, "feed-paged", "article", map[string]any{"feed_url": site.URL + "/feed.xml", "max_pages": 3}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 3 {
			t.Errorf("entries = %d, want 3", len(result.Entries))
		}
	})
	t.Run("an empty page", func(t *testing.T) {
		site := serve(t, func(page string) (string, int) {
			if page == "2" {
				return `<rss version="2.0"><channel><title>c</title></channel></rss>`, http.StatusOK
			}
			return one(page), http.StatusOK
		})
		result, err := probeWith(t, "feed-paged", "article", map[string]any{"feed_url": site.URL + "/feed.xml", "page_param": "paged"}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 1 {
			t.Errorf("entries = %d, want 1", len(result.Entries))
		}
	})
	t.Run("a first page that is missing", func(t *testing.T) {
		site := serve(t, func(string) (string, int) { return "", http.StatusNotFound })
		if _, err := probeWith(t, "feed-paged", "article", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25); err == nil {
			t.Error("probe succeeded")
		}
	})
}

func TestNumeric(t *testing.T) {
	t.Run("skips a gap", func(t *testing.T) {
		site := sitetest.Serve(t, "numeric-html")
		result, err := probeWith(t, "numeric", "page", map[string]any{"url_template": site.URL + "/comic/{n}.html", "start": 1, "max_consecutive_misses": 2}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if got := titles(result.Entries); !slices.Equal(got, []string{"Number 1", "Number 2", "Number 4", "Number 5"}) {
			t.Errorf("titles = %v", got)
		}
	})
	t.Run("too many misses end the archive", func(t *testing.T) {
		site := sitetest.Serve(t, "numeric-html")
		result, err := probeWith(t, "numeric", "page", map[string]any{"url_template": site.URL + "/comic/{n}.html", "start": 1, "max_consecutive_misses": 1}, 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 2 {
			t.Errorf("entries = %d, want 2", len(result.Entries))
		}
	})
	t.Run("json dates and nested fields", func(t *testing.T) {
		site := sitetest.Serve(t, "numeric-json")
		site.SetPage("/9/info.0.json", `{"data":{"title":"Nested","published":"2020-03-04"},"img":"https://imgs.example/9.png"}`)
		result, err := probeWith(t, "numeric", "page", map[string]any{
			"url_template": site.URL + "/{n}/info.0.json", "start": 9, "format": "json",
			"fields": map[string]any{"title": "data.title", "date": "data.published", "image": "img"},
		}, 1)
		if err != nil {
			t.Fatal(err)
		}
		e := result.Entries[0]
		if e.Title != "Nested" || e.Date == nil || e.Date.Format("2006-01-02") != "2020-03-04" {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("json with parts of a date", func(t *testing.T) {
		site := sitetest.Serve(t, "numeric-json")
		result, err := probeWith(t, "numeric", "page", map[string]any{
			"url_template": site.URL + "/{n}/info.0.json", "start": 2, "format": "json",
			"fields": map[string]any{"title": "safe_title", "image": "img", "alt_text": "alt", "year": "year", "month": "month", "day": "day"},
		}, 1)
		if err != nil {
			t.Fatal(err)
		}
		e := result.Entries[0]
		if e.Title != "Petit Trees" || e.AltText != "Alt 2" || e.Date == nil || e.Date.Format("2006-01-02") != "2006-01-02" {
			t.Errorf("entry = %+v", e)
		}
	})
	t.Run("a broken JSON document", func(t *testing.T) {
		site := sitetest.Serve(t, "numeric-json")
		site.SetPage("/1/info.0.json", "not json")
		if _, err := probeWith(t, "numeric", "page", map[string]any{
			"url_template": site.URL + "/{n}/info.0.json", "start": 1, "format": "json", "fields": map[string]any{"title": "safe_title"},
		}, 1); err == nil {
			t.Error("probe accepted broken JSON")
		}
	})
}

func TestFullTextIsOptIn(t *testing.T) {
	site := sitetest.Serve(t, "wordpress-blog")
	off, err := probeWith(t, "crawl", "article", map[string]any{"start_url": site.URL + "/1/"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if off.Entries[0].ContentHTML != "" {
		t.Errorf("stored text without full_text: %q", off.Entries[0].ContentHTML)
	}
	on, err := probeWith(t, "crawl", "article", map[string]any{"start_url": site.URL + "/1/", "full_text": true}, 1)
	if err != nil {
		t.Fatal(err)
	}
	content := on.Entries[0].ContentHTML
	if !strings.Contains(content, "The body of this post") {
		t.Errorf("content = %q", content)
	}
	for _, bad := range []string{"<script", "onclick", "<iframe"} {
		if strings.Contains(content, bad) {
			t.Errorf("content kept %s", bad)
		}
	}
}

func TestEpisodePagesHeadOnlyWhenNeeded(t *testing.T) {
	page := func(tags string) string { return `<title>Show</title><audio ` + tags + `></audio>` }
	var heads int
	serve := func(t *testing.T, html string) *sitetest.Site {
		t.Helper()
		heads = 0
		site := sitetest.Serve(t, "podcast")
		site.SetPage("/show", html)
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.Method == http.MethodHead {
				heads++
				w.Header().Set("Content-Type", "audio/mpeg")
				w.Header().Set("Content-Length", "1234")
				return true
			}
			return false
		})
		return site
	}

	site := serve(t, page(`src="/a.mp3"`))
	result, err := probeWith(t, "crawl", "episode", map[string]any{"start_url": site.URL + "/show"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	enclosure := result.Entries[0].Enclosures[0]
	if heads != 1 || enclosure.Type != "audio/mpeg" || enclosure.Length != 1234 {
		t.Errorf("heads = %d, enclosure = %+v", heads, enclosure)
	}

	// A feed gives the type and length, so nothing needs asking.
	site = serve(t, "")
	if _, err := probeWith(t, "feed", "episode", map[string]any{"feed_url": site.URL + "/feed.xml"}, 25); err != nil {
		t.Fatal(err)
	}
	if heads != 0 {
		t.Errorf("heads = %d for a feed's enclosures", heads)
	}
}
