// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/mmcdole/gofeed"
)

// browser is someone using the website: it keeps their session, and the CSRF token
// from the last page it saw, the way a form would.
type browser struct {
	address, session, csrf string
}

type visit struct {
	status   int
	body     string
	location string
}

func (b *browser) do(t *testing.T, method, path string, form url.Values) visit {
	t.Helper()
	var body io.Reader
	if form != nil {
		if b.csrf != "" {
			form.Set("csrf", b.csrf)
		}
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(t.Context(), method, "http://"+b.address+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	// The cookie is Secure, and this is plain HTTP, so a cookie jar wouldn't send it.
	if b.session != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: b.session})
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie && c.MaxAge >= 0 {
			b.session = c.Value
		}
	}
	if match := csrfField.FindStringSubmatch(string(data)); match != nil {
		b.csrf = match[1]
	}
	return visit{status: resp.StatusCode, body: string(data), location: resp.Header.Get("Location")}
}

func (b *browser) get(t *testing.T, path string) visit {
	t.Helper()
	return b.do(t, http.MethodGet, path, nil)
}

// post submits a form and expects to be sent on, as every form here does when it works.
func (b *browser) post(t *testing.T, path string, form url.Values) string {
	t.Helper()
	got := b.do(t, http.MethodPost, path, form)
	if got.status != http.StatusSeeOther {
		t.Fatalf("POST %s: %d %s", path, got.status, got.body)
	}
	return got.location
}

var (
	addThis  = regexp.MustCompile(`action="(/add/[^/"]+/0)"`)
	feedPage = regexp.MustCompile(`href="https://andante\.example(/f/[^"]+)"`)
)

// The whole of what someone does the first time, through the website alone: set up
// the first admin, add a comic by its address, track it, and read the feed.
func TestFromLoginToAFeed(t *testing.T) {
	const token = "a-bootstrap-token-of-at-least-32-characters"
	catalogue := startCatalogue(t, build(t, "andante-catalogue"), storetest.DSN(t), crawlFlags...)
	catalogue.waitReady(t)
	server := startServer(t, buildServer(t), storetest.DSN(t), catalogue.flag(), "--demand-interval=50ms", "--feed-rate-limit=100000", "--bootstrap-token="+token)
	server.waitReady(t)
	site := sitetest.Serve(t, "discover-comic")
	b := &browser{address: server.public}

	if got := b.get(t, "/"); got.status != http.StatusSeeOther {
		t.Fatalf("home before logging in: %d", got.status)
	}
	invite := b.post(t, "/setup", url.Values{"token": {token}, "name": {"dave"}})
	if !strings.HasPrefix(invite, "/invite/") {
		t.Fatalf("setup sent us to %q", invite)
	}
	b.post(t, invite, url.Values{"password": {"a long enough phrase"}})
	if b.session == "" {
		t.Fatal("no session after setting a password")
	}
	if got := b.get(t, "/"); got.status != http.StatusOK || !strings.Contains(got.body, "dave") {
		t.Fatalf("home: %d %s", got.status, got.body)
	}

	b.get(t, "/add")
	job := b.post(t, "/add", url.Values{"url": {site.URL + "/"}})
	var found visit
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		found = b.get(t, job)
		if found.status != http.StatusOK || !strings.Contains(found.body, `http-equiv="refresh"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("still looking at %s. Logs:\n%s\n%s", site.URL, server.output(), catalogue.output())
		}
	}
	match := addThis.FindStringSubmatch(found.body)
	if found.status != http.StatusOK || match == nil || !strings.Contains(found.body, "The Comic") {
		t.Fatalf("what was found: %d %s", found.status, found.body)
	}
	track := b.post(t, match[1], url.Values{})
	if !strings.HasSuffix(track, "/track") {
		t.Fatalf("adding sent us to %q", track)
	}

	b.get(t, track)
	read := b.post(t, track, url.Values{"name": {"Mornings"}, "preset": {"daily"}, "at": {"07:00"}, "tz": {"UTC"}, "count": {"1"}})
	page := b.get(t, read)
	link := feedPage.FindStringSubmatch(page.body)
	if page.status != http.StatusOK || link == nil {
		t.Fatalf("the read: %d %s", page.status, page.body)
	}

	// The read can come before the server has the first page, so it's in the feed
	// as soon as the next sync brings it.
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		feed := fetchFeed(t, "http://"+server.public+link[1]+".rss")
		if len(feed.Items) == 1 && strings.HasSuffix(feed.Items[0].Link, "/1.html") {
			return
		}
		if len(feed.Items) > 0 || time.Now().After(deadline) {
			t.Fatalf("feed items = %+v. Logs:\n%s\n%s", feed.Items, server.output(), catalogue.output())
		}
	}
}

func fetchFeed(t *testing.T, address string) *gofeed.Feed {
	t.Helper()
	resp, err := http.Get(address)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed: %d", resp.StatusCode)
	}
	feed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return feed
}
