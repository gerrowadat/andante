// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
)

func newFetcher(clock *sitetest.Clock) *discovery.Fetcher {
	return discovery.NewFetcher(sitetest.FetcherOptions(clock))
}

func fetch(t *testing.T, f *discovery.Fetcher, url string) (*discovery.Response, error) {
	t.Helper()
	return f.Fetch(t.Context(), url, discovery.Validators{})
}

func TestSpacing(t *testing.T) {
	clock := sitetest.NewClock()
	f := newFetcher(clock)
	site := sitetest.Serve(t, "comiccontrol")
	start := clock.Now()
	for _, page := range []string{"/1.html", "/2.html", "/3.html"} {
		if _, err := fetch(t, f, site.URL+page); err != nil {
			t.Fatal(err)
		}
	}
	// robots.txt then three pages: three gaps of the interval.
	if got := clock.Now().Sub(start); got != 15*time.Second {
		t.Errorf("elapsed = %s, want 15s", got)
	}
}

func TestHostsDoNotShareSpacing(t *testing.T) {
	clock := sitetest.NewClock()
	f := newFetcher(clock)
	a, b := sitetest.Serve(t, "comiccontrol"), sitetest.Serve(t, "cycle")
	if _, err := fetch(t, f, a.URL+"/1.html"); err != nil {
		t.Fatal(err)
	}
	before := len(clock.Sleeps())
	if _, err := fetch(t, f, b.URL+"/1.html"); err != nil {
		t.Fatal(err)
	}
	// One wait: between b's robots.txt and its page.
	if got := len(clock.Sleeps()) - before; got != 1 {
		t.Errorf("sleeps for the second host = %d, want 1", got)
	}
}

func TestOneRequestInFlightPerHost(t *testing.T) {
	var inFlight, highest atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		now := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			top := highest.Load()
			if now <= top || highest.CompareAndSwap(top, now) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}))
	defer srv.Close()

	f := newFetcher(sitetest.NewClock())
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := fetch(t, f, srv.URL+"/page"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if got := highest.Load(); got != 1 {
		t.Errorf("most requests in flight = %d, want 1", got)
	}
}

func TestWaitingForAHostStopsWithItsContext(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			close(entered)
			<-release
		}
	}))
	defer srv.Close()
	defer close(release)

	f := newFetcher(sitetest.NewClock())
	go func() { _, _ = f.Fetch(context.Background(), srv.URL+"/slow", discovery.Validators{}) }()
	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, err := f.Fetch(ctx, srv.URL+"/second", discovery.Validators{}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Fetch = %v, want the context's error", err)
	}
}

func TestRetryAfter(t *testing.T) {
	clock := sitetest.NewClock()
	tests := []struct {
		name, header string
		want         time.Duration
	}{
		{"seconds", "120", 120 * time.Second},
		{"date", clock.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat), 10 * time.Minute},
		{"capped", "999999", 6 * time.Hour},
	}
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, tt := range tests {
			t.Run(strconv.Itoa(status)+" "+tt.name, func(t *testing.T) {
				clock := sitetest.NewClock()
				f := newFetcher(clock)
				site := sitetest.Serve(t, "rate-limited")
				site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
					if r.URL.Path != "/1.html" {
						return false
					}
					w.Header().Set("Retry-After", tt.header)
					w.WriteHeader(status)
					return true
				})

				start := clock.Now()
				_, err := fetch(t, f, site.URL+"/1.html")
				var backoff *discovery.BackoffError
				if !errors.As(err, &backoff) {
					t.Fatalf("Fetch = %v, want a BackoffError", err)
				}
				if got := backoff.Until.Sub(start); got < tt.want-time.Second*2 || got > tt.want+time.Second*6 {
					t.Errorf("waits %s, want about %s", got, tt.want)
				}

				site.Intercept(nil)
				before := site.Count("/1.html")
				if _, err := fetch(t, f, site.URL+"/1.html"); !errors.As(err, &backoff) {
					t.Errorf("second Fetch = %v, want it refused", err)
				}
				if site.Count("/1.html") != before {
					t.Error("the host was contacted during its backoff")
				}

				clock.Advance(tt.want + 10*time.Second)
				if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
					t.Errorf("Fetch after the wait = %v", err)
				}
			})
		}
	}
}

func TestExponentialBackoff(t *testing.T) {
	clock := sitetest.NewClock()
	f := newFetcher(clock)
	var failing atomic.Bool
	failing.Store(true)
	site := sitetest.Serve(t, "comiccontrol")
	site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/robots.txt" || !failing.Load() {
			return false
		}
		w.WriteHeader(http.StatusInternalServerError)
		return true
	})

	var waits []time.Duration
	for range 15 {
		now := clock.Now()
		_, err := fetch(t, f, site.URL+"/1.html")
		var backoff *discovery.BackoffError
		if !errors.As(err, &backoff) {
			t.Fatalf("Fetch = %v", err)
		}
		waits = append(waits, backoff.Until.Sub(now))
		clock.Advance(backoff.Until.Sub(clock.Now()) + time.Second)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] < waits[i-1] {
			t.Errorf("wait %d shrank: %v", i, waits)
		}
	}
	// The first wait also holds the gap after robots.txt, so start from the second.
	if waits[2] != 2*waits[1] {
		t.Errorf("waits do not double: %v", waits)
	}
	if last := waits[len(waits)-1]; last > 6*time.Hour+6*time.Second || last < 5*time.Hour {
		t.Errorf("last wait = %s, want the 6h cap", last)
	}

	failing.Store(false)
	if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
		t.Fatalf("Fetch after recovery = %v", err)
	}
	failing.Store(true)
	now := clock.Now()
	_, err := fetch(t, f, site.URL+"/1.html")
	var backoff *discovery.BackoffError
	if !errors.As(err, &backoff) {
		t.Fatal(err)
	}
	if got := backoff.Until.Sub(now); got > time.Minute {
		t.Errorf("wait after a recovery = %s, want it back at the start", got)
	}
}

func TestRobots(t *testing.T) {
	t.Run("disallowed", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		site := sitetest.Serve(t, "robots-disallow")
		if _, err := fetch(t, f, site.URL+"/comics/1.html"); !errors.Is(err, discovery.ErrDisallowed) {
			t.Errorf("Fetch = %v, want ErrDisallowed", err)
		}
		if site.Count("/comics/1.html") != 0 {
			t.Error("a disallowed page was requested")
		}
	})
	t.Run("missing allows everything", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		site := sitetest.Serve(t, "comiccontrol")
		if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
			t.Errorf("Fetch = %v", err)
		}
	})
	t.Run("server error backs off", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		site := sitetest.Serve(t, "comiccontrol")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		})
		var backoff *discovery.BackoffError
		if _, err := fetch(t, f, site.URL+"/1.html"); !errors.As(err, &backoff) {
			t.Errorf("Fetch = %v, want a BackoffError", err)
		}
		if site.Count("/1.html") != 0 {
			t.Error("a page was requested without a robots.txt answer")
		}
	})
	t.Run("cached for a day", func(t *testing.T) {
		clock := sitetest.NewClock()
		f := newFetcher(clock)
		site := sitetest.Serve(t, "comiccontrol")
		for range 3 {
			if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
				t.Fatal(err)
			}
		}
		if got := site.Count("/robots.txt"); got != 1 {
			t.Errorf("robots.txt fetched %d times, want 1", got)
		}
		clock.Advance(25 * time.Hour)
		if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
			t.Fatal(err)
		}
		if got := site.Count("/robots.txt"); got != 2 {
			t.Errorf("robots.txt fetched %d times after the TTL, want 2", got)
		}
	})
}

func TestConditionalRequests(t *testing.T) {
	var gotETag, gotModified atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			return
		}
		gotETag.Store(r.Header.Get("If-None-Match"))
		gotModified.Store(r.Header.Get("If-Modified-Since"))
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	f := newFetcher(sitetest.NewClock())
	resp, err := f.Fetch(t.Context(), srv.URL+"/p", discovery.Validators{ETag: `"v1"`, LastModified: "yesterday"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.NotModified {
		t.Error("NotModified = false")
	}
	if gotETag.Load() != `"v1"` || gotModified.Load() != "yesterday" {
		t.Errorf("sent %v, %v", gotETag.Load(), gotModified.Load())
	}
}

func TestRedirects(t *testing.T) {
	t.Run("followed", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		site := sitetest.Serve(t, "comiccontrol")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/old" {
				http.Redirect(w, r, "/1.html", http.StatusMovedPermanently)
				return true
			}
			return false
		})
		resp, err := fetch(t, f, site.URL+"/old")
		if err != nil {
			t.Fatal(err)
		}
		if resp.URL != site.URL+"/1.html" {
			t.Errorf("URL = %q", resp.URL)
		}
	})
	t.Run("limited to five hops", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				return
			}
			n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
			http.Redirect(w, r, "/"+strconv.Itoa(n+1), http.StatusFound)
		}))
		defer srv.Close()
		if _, err := fetch(t, f, srv.URL+"/0"); !errors.Is(err, discovery.ErrTooManyHops) {
			t.Errorf("Fetch = %v, want ErrTooManyHops", err)
		}
	})
	t.Run("only to http", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/robots.txt" {
				w.Header().Set("Location", "file:///etc/passwd")
				w.WriteHeader(http.StatusFound)
			}
		}))
		defer srv.Close()
		if _, err := fetch(t, f, srv.URL+"/a"); !errors.Is(err, discovery.ErrScheme) {
			t.Errorf("Fetch = %v, want ErrScheme", err)
		}
	})
	t.Run("each hop obeys robots.txt", func(t *testing.T) {
		f := newFetcher(sitetest.NewClock())
		site := sitetest.Serve(t, "robots-disallow")
		site.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/old" {
				http.Redirect(w, r, "/comics/1.html", http.StatusFound)
				return true
			}
			return false
		})
		if _, err := fetch(t, f, site.URL+"/old"); !errors.Is(err, discovery.ErrDisallowed) {
			t.Errorf("Fetch = %v, want ErrDisallowed", err)
		}
	})
}

func TestBodyLimit(t *testing.T) {
	opts := sitetest.FetcherOptions(sitetest.NewClock())
	opts.MaxBody = 10
	f := discovery.NewFetcher(opts)
	site := sitetest.Serve(t, "comiccontrol")
	if _, err := fetch(t, f, site.URL+"/1.html"); !errors.Is(err, discovery.ErrBodyTooLarge) {
		t.Errorf("Fetch = %v, want ErrBodyTooLarge", err)
	}
}

func TestUserAgent(t *testing.T) {
	var agents sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		agents.Store(r.URL.Path, r.UserAgent())
	}))
	defer srv.Close()
	if _, err := fetch(t, newFetcher(sitetest.NewClock()), srv.URL+"/p"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/p", "/robots.txt"} {
		if got, _ := agents.Load(path); got != "andante-test" {
			t.Errorf("%s User-Agent = %v", path, got)
		}
	}
}

func TestErrorsAndSchemes(t *testing.T) {
	f := newFetcher(sitetest.NewClock())
	site := sitetest.Serve(t, "comiccontrol")
	var status *discovery.StatusError
	if _, err := fetch(t, f, site.URL+"/missing.html"); !errors.As(err, &status) || status.Status != http.StatusNotFound {
		t.Errorf("Fetch(404) = %v", err)
	}
	if _, err := fetch(t, f, "ftp://comic.example/1"); !errors.Is(err, discovery.ErrScheme) {
		t.Errorf("Fetch(ftp) = %v", err)
	}
}

func TestSSRFGuard(t *testing.T) {
	t.Run("loopback refused by default", func(t *testing.T) {
		opts := sitetest.FetcherOptions(sitetest.NewClock())
		opts.AddressAllowed = nil
		f := discovery.NewFetcher(opts)
		site := sitetest.Serve(t, "comiccontrol")
		if _, err := fetch(t, f, site.URL+"/1.html"); !errors.Is(err, discovery.ErrAddress) {
			t.Errorf("Fetch = %v, want ErrAddress", err)
		}
		if got := len(site.Requests()); got != 0 {
			t.Errorf("the guarded host got %d requests", got)
		}
	})
	t.Run("the flag allows it", func(t *testing.T) {
		opts := sitetest.FetcherOptions(sitetest.NewClock())
		opts.AddressAllowed, opts.AllowPrivate = nil, true
		f := discovery.NewFetcher(opts)
		site := sitetest.Serve(t, "comiccontrol")
		if _, err := fetch(t, f, site.URL+"/1.html"); err != nil {
			t.Errorf("Fetch = %v", err)
		}
	})
	t.Run("a redirect to a private address is refused", func(t *testing.T) {
		internal := sitetest.Serve(t, "comiccontrol")

		// The first host stands in for a public one: addresses are allowed until
		// it has answered, as a real public host would be.
		var private atomic.Bool
		opts := sitetest.FetcherOptions(sitetest.NewClock())
		opts.AddressAllowed = func(netip.Addr) bool { return !private.Load() }
		f := discovery.NewFetcher(opts)

		public := sitetest.Serve(t, "comiccontrol")
		public.Intercept(func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path == "/go" {
				private.Store(true)
				http.Redirect(w, r, internal.URL+"/1.html", http.StatusFound)
				return true
			}
			return false
		})

		if _, err := fetch(t, f, public.URL+"/go"); !errors.Is(err, discovery.ErrAddress) {
			t.Errorf("Fetch = %v, want ErrAddress", err)
		}
		if got := len(internal.Requests()); got != 0 {
			t.Errorf("the private host got %d requests", got)
		}
	})
}

func TestPublicAddress(t *testing.T) {
	tests := map[string]bool{
		"8.8.8.8":                true,
		"2606:4700::1111":        true,
		"127.0.0.1":              false,
		"::1":                    false,
		"10.1.2.3":               false,
		"172.16.0.1":             false,
		"192.168.1.1":            false,
		"169.254.169.254":        false, // Cloud metadata.
		"100.100.100.100":        false, // Tailscale.
		"0.0.0.0":                false,
		"fc00::1":                false,
		"fe80::1":                false,
		"::ffff:127.0.0.1":       false, // IPv4-mapped.
		"::ffff:169.254.169.254": false,
		"64:ff9b::7f00:1":        false, // NAT64 of loopback.
		"224.0.0.1":              false,
		"255.255.255.255":        false,
	}
	for address, want := range tests {
		if got := discovery.PublicAddress(netip.MustParseAddr(address)); got != want {
			t.Errorf("PublicAddress(%s) = %v, want %v", address, got, want)
		}
	}
}
