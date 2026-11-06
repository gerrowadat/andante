// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package sitetest serves the fixture sites in testdata/sites, and gives tests a
// fetcher that can only reach them.
package sitetest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery"
)

// Site is a running fixture site.
type Site struct {
	URL string

	mu        sync.Mutex
	pages     map[string]string
	requests  []string
	intercept func(http.ResponseWriter, *http.Request) bool
}

// Serve starts the named fixture site. A site's pages can change during a test,
// as a live serial's do.
func Serve(t *testing.T, name string) *Site {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot find the fixtures")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "testdata", "sites", name)
	files := http.FileServer(http.Dir(dir))

	s := &Site{pages: map[string]string{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.URL.Path)
		intercept := s.intercept
		page, replaced := s.pages[r.URL.Path]
		s.mu.Unlock()

		if intercept != nil && intercept(w, r) {
			return
		}
		if replaced {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(page))
			return
		}
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// SetPage adds or replaces a page.
func (s *Site) SetPage(path, html string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pages[path] = html
}

// Intercept handles requests first. A func that returns true has answered.
func (s *Site) Intercept(f func(http.ResponseWriter, *http.Request) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.intercept = f
}

// Requests lists the paths requested so far.
func (s *Site) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Count returns how many times a path was requested.
func (s *Site) Count(path string) int {
	n := 0
	for _, p := range s.Requests() {
		if p == path {
			n++
		}
	}
	return n
}

// Clock is a fake discovery.Clock. Sleeping moves it on at once and records the
// wait, so tests can run hours of politeness instantly.
type Clock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

// NewClock returns a Clock set to now.
func NewClock() *Clock { return &Clock{now: time.Now()} }

// Now implements discovery.Clock.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Sleep implements discovery.Clock.
func (c *Clock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.sleeps = append(c.sleeps, d)
	return nil
}

// Advance moves the clock on.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Sleeps lists the waits so far.
func (c *Clock) Sleeps() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.sleeps...)
}

// FetcherOptions returns options whose fetcher can only dial loopback, so a test
// that strays onto the real network fails instead of quietly using it.
func FetcherOptions(clock discovery.Clock) discovery.FetcherOptions {
	return discovery.FetcherOptions{
		UserAgent:      "andante-test",
		Interval:       5 * time.Second,
		Timeout:        5 * time.Second,
		BackoffCap:     6 * time.Hour,
		RobotsTTL:      24 * time.Hour,
		MaxBody:        5 << 20,
		Clock:          clock,
		AddressAllowed: netip.Addr.IsLoopback,
	}
}
