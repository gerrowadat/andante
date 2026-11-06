// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package strategies_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/prometheus/client_golang/prometheus"
)

func TestCrawlValidate(t *testing.T) {
	tests := []struct {
		name, config string
		valid        bool
	}{
		{"minimal", `{"strategy":"crawl","start_url":"https://comic.example/1"}`, true},
		{"selectors", `{"strategy":"crawl","start_url":"http://comic.example/","selectors":{"next":"a.next"}}`, true},
		{"not JSON", `nope`, false},
		{"unknown field", `{"strategy":"crawl","start_url":"https://comic.example/","bogus":1}`, false},
		{"unknown selector", `{"strategy":"crawl","start_url":"https://comic.example/","selectors":{"bogus":"a"}}`, false},
		{"bad selector", `{"strategy":"crawl","start_url":"https://comic.example/","selectors":{"next":"a[["}}`, false},
		{"no start URL", `{"strategy":"crawl"}`, false},
		{"relative start URL", `{"strategy":"crawl","start_url":"/1"}`, false},
		{"non-http start URL", `{"strategy":"crawl","start_url":"file:///etc/passwd"}`, false},
		{"unparseable start URL", `{"strategy":"crawl","start_url":"http://[::1"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := strategies.Crawl{}.Validate(json.RawMessage(tt.config))
			if (err == nil) != tt.valid {
				t.Errorf("Validate() = %v, want valid %v", err, tt.valid)
			}
		})
	}
}

func config(t *testing.T, startURL string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"strategy": "crawl", "start_url": startURL})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func probe(t *testing.T, startURL string, limit int) (discovery.ProbeResult, error) {
	t.Helper()
	registry := discovery.NewRegistry(strategies.Crawl{})
	fetcher := discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))
	prober := discovery.NewProber(registry, fetcher, discovery.NewMetrics(prometheus.NewRegistry()))
	return prober.Probe(t.Context(), discovery.ProbeRequest{
		Strategy: "crawl", Config: config(t, startURL), Kind: "page", Limit: limit,
	})
}

func titles(entries []discovery.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Title)
	}
	return out
}

func warned(warnings []string, want string) bool {
	return strings.Contains(strings.Join(warnings, "\n"), want)
}

func TestProbeFixtures(t *testing.T) {
	tests := []struct {
		site, start string
		titles      []string
		dated       float64
		warning     string
	}{
		{
			site: "comiccontrol", start: "/1.html",
			titles: []string{
				"Harbour Lights - Page 1", "Harbour Lights - Page 2", "Harbour Lights - Page 3",
				"Harbour Lights - Page 4", "Harbour Lights - Page 5",
			},
			warning: "next link points to the current page",
		},
		{
			site: "wordpress-webcomic", start: "/comic/1/",
			titles: []string{"Strip 1", "Strip 2", "Strip 3", "Strip 4", "Strip 5"},
			dated:  1, warning: "no next link",
		},
		{
			site: "jsonld-only", start: "/1.html",
			titles: []string{"Episode 1", "Episode 2", "Episode 3", "Episode 4", "Episode 5"},
			dated:  1, warning: "no next link",
		},
		{
			site: "missing-next", start: "/1.html",
			titles:  []string{"Gap 1", "Gap 2", "Gap 3"},
			warning: "no next link on",
		},
	}
	for _, tt := range tests {
		t.Run(tt.site, func(t *testing.T) {
			site := sitetest.Serve(t, tt.site)
			result, err := probe(t, site.URL+tt.start, 25)
			if err != nil {
				t.Fatal(err)
			}
			if got := titles(result.Entries); !slices.Equal(got, tt.titles) {
				t.Errorf("titles = %v, want %v", got, tt.titles)
			}
			if result.DatedShare != tt.dated {
				t.Errorf("dated share = %v, want %v", result.DatedShare, tt.dated)
			}
			if !warned(result.Warnings, tt.warning) {
				t.Errorf("warnings = %v, want %q", result.Warnings, tt.warning)
			}
		})
	}
}

func TestProbeLimit(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	result, err := probe(t, site.URL+"/1.html", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 2 {
		t.Errorf("entries = %d, want 2", len(result.Entries))
	}
	if got := site.Count("/3.html"); got != 0 {
		t.Errorf("/3.html requested %d times past the limit", got)
	}
}

func TestProbeBadSites(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		site := sitetest.Serve(t, "cycle")
		if _, err := probe(t, site.URL+"/1.html", 25); !errors.Is(err, discovery.ErrCycle) {
			t.Errorf("probe = %v, want ErrCycle", err)
		}
	})
	t.Run("cycle through another URL", func(t *testing.T) {
		site := sitetest.Serve(t, "comiccontrol")
		page := func(title, next string) string {
			return `<title>` + title + `</title><img id="cc-comic" src="/same.png"><a rel="next" href="` + next + `">n</a>`
		}
		site.SetPage("/a", page("Same", "/b"))
		site.SetPage("/b", page("Other", "/a?again=1"))
		site.SetPage("/a?again=1", "")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.RawQuery == "again=1" {
				_, _ = w.Write([]byte(page("Same", "/b")))
				return true
			}
			return false
		})
		if _, err := probe(t, site.URL+"/a", 25); !errors.Is(err, discovery.ErrCycle) {
			t.Errorf("probe = %v, want ErrCycle", err)
		}
	})
	t.Run("robots disallow", func(t *testing.T) {
		site := sitetest.Serve(t, "robots-disallow")
		if _, err := probe(t, site.URL+"/comics/1.html", 25); !errors.Is(err, discovery.ErrDisallowed) {
			t.Errorf("probe = %v, want ErrDisallowed", err)
		}
	})
	t.Run("rate limited partway", func(t *testing.T) {
		site := sitetest.Serve(t, "rate-limited")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/3.html" {
				return false
			}
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		})
		result, err := probe(t, site.URL+"/1.html", 25)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Entries) != 2 {
			t.Errorf("entries = %d, want the 2 before the limit", len(result.Entries))
		}
		if !warned(result.Warnings, "stopped early") {
			t.Errorf("warnings = %v", result.Warnings)
		}
	})
	t.Run("rate limited from the start", func(t *testing.T) {
		site := sitetest.Serve(t, "rate-limited")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/robots.txt" {
				return false
			}
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return true
		})
		var backoff *discovery.BackoffError
		if _, err := probe(t, site.URL+"/1.html", 25); !errors.As(err, &backoff) {
			t.Errorf("probe = %v, want a BackoffError", err)
		}
	})
	t.Run("site logo", func(t *testing.T) {
		site := sitetest.Serve(t, "comiccontrol")
		for _, n := range []string{"1", "2", "3"} {
			next := `<a rel="next" href="/l` + string(n[0]+1) + `">n</a>`
			if n == "3" {
				next = ""
			}
			site.SetPage("/l"+n, `<title>Page `+n+`</title><img id="cc-comic" src="/logo.png">`+next)
		}
		result, err := probe(t, site.URL+"/l1", 25)
		if err != nil {
			t.Fatal(err)
		}
		if !warned(result.Warnings, "may be a site logo") {
			t.Errorf("warnings = %v", result.Warnings)
		}
	})
}

type plainFetcher struct {
	f *discovery.Fetcher
	v discovery.Validators
}

func (p plainFetcher) Fetch(ctx context.Context, url string) (*discovery.Response, error) {
	return p.f.Fetch(ctx, url, p.v)
}

func TestNextResumesFromItsCursor(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	fetcher := plainFetcher{f: discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))}
	src := discovery.Source{DefaultKind: "page", Config: config(t, site.URL+"/1.html")}

	first, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 2 || first.LiveEnd {
		t.Fatalf("first batch: %d entries, live end %v", len(first.Entries), first.LiveEnd)
	}

	second, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, first.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := second.Entries[0].Title; got != "Harbour Lights - Page 3" {
		t.Errorf("resumed at %q", got)
	}
	if len(second.Entries) != 3 || !second.LiveEnd {
		t.Errorf("second batch: %d entries, live end %v", len(second.Entries), second.LiveEnd)
	}
}

func TestLiveEndRecheck(t *testing.T) {
	site := sitetest.Serve(t, "wordpress-webcomic")
	fetcher := plainFetcher{f: discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))}
	src := discovery.Source{DefaultKind: "page", Config: config(t, site.URL+"/comic/1/")}

	indexed, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !indexed.LiveEnd {
		t.Fatal("did not reach the live end")
	}

	quiet, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, indexed.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(quiet.Entries) != 0 || !quiet.LiveEnd {
		t.Errorf("no change: %d entries, live end %v", len(quiet.Entries), quiet.LiveEnd)
	}
	if len(quiet.Updates) != 1 {
		t.Errorf("updates = %d, want the re-fetched page", len(quiet.Updates))
	}

	last := `<title>Strip 5</title><meta property="og:title" content="Strip 5, recoloured"><a rel="next" href="/comic/6/">n</a>`
	site.SetPage("/comic/5/", last)
	site.SetPage("/comic/6/", `<title>Strip 6</title><div class="webcomic-image"><img src="/6.png"></div>`)
	grown, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, quiet.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(grown.Entries) != 1 || grown.Entries[0].Title != "Strip 6" {
		t.Fatalf("grown = %v", titles(grown.Entries))
	}
	if len(grown.Updates) != 1 || grown.Updates[0].Title != "Strip 5, recoloured" {
		t.Errorf("updates = %v", titles(grown.Updates))
	}
}

func TestNotModifiedAtTheLiveEnd(t *testing.T) {
	site := sitetest.Serve(t, "wordpress-webcomic")
	var conditional atomic.Int32
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("If-None-Match") == "v1" {
			conditional.Add(1)
			w.WriteHeader(http.StatusNotModified)
			return true
		}
		return false
	})
	inner := discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))
	src := discovery.Source{DefaultKind: "page", Config: config(t, site.URL+"/comic/1/")}
	indexed, err := strategies.Crawl{}.Next(t.Context(), plainFetcher{f: inner}, src, nil, 10)
	if err != nil {
		t.Fatal(err)
	}

	quiet, err := strategies.Crawl{}.Next(t.Context(), plainFetcher{f: inner, v: discovery.Validators{ETag: "v1"}}, src, indexed.Cursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !quiet.LiveEnd || len(quiet.Entries)+len(quiet.Updates) != 0 {
		t.Errorf("304: %+v", quiet)
	}
	if conditional.Load() != 1 {
		t.Errorf("conditional requests = %d", conditional.Load())
	}
}

func TestKeepsAPartialBatch(t *testing.T) {
	site := sitetest.Serve(t, "rate-limited")
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/3.html" {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		}
		return false
	})
	fetcher := plainFetcher{f: discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))}
	src := discovery.Source{DefaultKind: "page", Config: config(t, site.URL+"/1.html")}
	batch, err := strategies.Crawl{}.Next(t.Context(), fetcher, src, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 2 || batch.LiveEnd {
		t.Errorf("%d entries, live end %v", len(batch.Entries), batch.LiveEnd)
	}
	if !strings.Contains(string(batch.Cursor), "3.html") {
		t.Errorf("cursor = %s, want it at the failed page", batch.Cursor)
	}
}

func TestKindWithNoExtractorFails(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	fetcher := plainFetcher{f: discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))}
	src := discovery.Source{DefaultKind: "article", Config: config(t, site.URL+"/1.html")}
	if _, err := (strategies.Crawl{}).Next(t.Context(), fetcher, src, nil, 1); err == nil {
		t.Error("Next succeeded with no extractor for the kind")
	}
}

func TestMetricsCountProbes(t *testing.T) {
	site := sitetest.Serve(t, "comiccontrol")
	reg := prometheus.NewRegistry()
	prober := discovery.NewProber(discovery.NewRegistry(strategies.Crawl{}),
		discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock())), discovery.NewMetrics(reg))
	_, err := prober.Probe(t.Context(), discovery.ProbeRequest{Strategy: "crawl", Config: config(t, site.URL+"/1.html"), Kind: "page", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{"andante_probe_runs_total", "andante_crawl_requests_total"} {
		if !names[want] {
			t.Errorf("no %s in %v", want, names)
		}
	}
}
