// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/mmcdole/gofeed"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const dailyAt7 = "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0"

type feedFixture struct {
	actor    store.Actor
	store    *store.Store
	reads    *reads.Service
	server   *httptest.Server
	registry *prometheus.Registry
	logs     lockedBuffer

	mu  sync.Mutex
	now time.Time
}

func (f *feedFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *feedFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func newFeedFixture(t *testing.T, ratePerHour int) *feedFixture {
	t.Helper()
	st := storetest.New(t)
	f := &feedFixture{store: st, registry: prometheus.NewRegistry(), now: time.Now().UTC().Truncate(time.Second)}
	f.reads = reads.New(reads.Options{Store: st, DefaultTimezone: "UTC", MinDatedShare: 0.9, Now: f.clock})
	f.server = httptest.NewServer(NewPublic(PublicOptions{
		Store: st, Reads: f.reads, BaseURL: "https://andante.example/", RatePerHour: ratePerHour,
		Registry: f.registry, Logger: slog.New(slog.NewTextHandler(&f.logs, nil)), Now: f.clock,
	}))
	t.Cleanup(f.server.Close)
	f.actor, _ = storetest.User(t, st, "reader", false)
	return f
}

func (f *feedFixture) source(t *testing.T, kind string) db.Source {
	t.Helper()
	s, err := storetest.AddSource(t, f.store, db.UpsertSourceParams{
		Slug: "s-" + strconv.FormatInt(time.Now().UnixNano(), 36), Title: "Comic", HomeUrl: "https://publisher.example/", DefaultKind: kind,
		Strategy: "crawl", Config: []byte(`{"strategy":"crawl","start_url":"https://publisher.example/1"}`), State: "tail",
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func (f *feedFixture) entries(t *testing.T, source db.Source, n int, extra string) {
	t.Helper()
	for i := range n {
		_, err := f.store.Pool().Exec(t.Context(), `INSERT INTO entries (id, source_id, seq, kind, url, title, images, discovered_at, available_at)
			VALUES ((SELECT coalesce(max(id), 0) + 1 FROM entries), $1, $2, $3, $4, $5, '{}', $6, $6)`+extra,
			source.ID, i, source.DefaultKind, fmt.Sprintf("https://publisher.example/%d", i), fmt.Sprintf("Entry %d", i), f.clock().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
	}
}

func (f *feedFixture) read(t *testing.T, source db.Source) reads.View {
	t.Helper()
	v, err := f.reads.Create(t.Context(), reads.CreateParams{
		UserID: f.actor.UserID, SourceID: source.ID, Name: "Mornings", Initial: 3,
		Pacing: schedule.RRulePacing{RRule: dailyAt7, TimeZone: "UTC", PerRelease: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f *feedFixture) token(t *testing.T, read int64, role string) string {
	t.Helper()
	_, raw, err := f.store.CreateFeedToken(t.Context(), store.Holder, read, role, "label")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (f *feedFixture) get(t *testing.T, path string, headers ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func parse(t *testing.T, body string) *gofeed.Feed {
	t.Helper()
	parsed, err := gofeed.NewParser().ParseString(body)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	return parsed
}

func items(parsed *gofeed.Feed) []string {
	out := make([]string, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		out = append(out, item.GUID+"|"+item.Title+"|"+item.Published)
	}
	return out
}

func TestViewersShareItemsAndTheOwnerHasAControl(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	read := f.read(t, s).Read.ID
	alex, sam, owner := f.token(t, read, feed.RoleViewer), f.token(t, read, feed.RoleViewer), f.token(t, read, feed.RoleOwner)

	_, alexBody := f.get(t, "/f/"+alex+".atom")
	_, samBody := f.get(t, "/f/"+sam+".atom")
	_, ownerBody := f.get(t, "/f/"+owner+".atom")

	if got, want := items(parse(t, alexBody)), items(parse(t, samBody)); !slices.Equal(got, want) {
		t.Errorf("viewers differ:\n%v\n%v", got, want)
	}
	if got, want := items(parse(t, ownerBody)), items(parse(t, alexBody)); !slices.Equal(got, want) {
		t.Errorf("the owner's items differ:\n%v\n%v", got, want)
	}
	if len(parse(t, alexBody).Items) != 3 {
		t.Errorf("items = %d, want 3", len(parse(t, alexBody).Items))
	}
	if !strings.Contains(ownerBody, "/f/"+owner+"/next") {
		t.Error("the owner has no control link")
	}
	for _, body := range []string{alexBody, samBody} {
		if strings.Contains(body, "/next") || strings.Contains(body, owner) {
			t.Error("a viewer's feed holds an owner link")
		}
	}
}

func TestFormats(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	token := f.token(t, f.read(t, s).Read.ID, feed.RoleViewer)
	for extension, contentType := range map[string]string{
		"atom": "application/atom+xml", "rss": "application/rss+xml", "json": "application/feed+json",
	} {
		resp, body := f.get(t, "/f/"+token+"."+extension)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: %d", extension, resp.StatusCode)
		}
		if got := resp.Header.Get("Content-Type"); got != contentType {
			t.Errorf("%s: Content-Type = %q", extension, got)
		}
		if len(parse(t, body).Items) != 3 {
			t.Errorf("%s: wrong items", extension)
		}
		if !strings.Contains(body, "https://andante.example/f/"+token+"."+extension) {
			t.Errorf("%s: no self link", extension)
		}
	}
}

func TestItemsNeverChange(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 20, "")
	view := f.read(t, s)
	token := f.token(t, view.Read.ID, feed.RoleViewer)
	path := "/f/" + token + ".atom"

	f.advance(72 * time.Hour)
	_, body := f.get(t, path)
	before := items(parse(t, body))

	adjust := func(a schedule.Adjustment) {
		t.Helper()
		if _, err := f.reads.Adjust(t.Context(), store.Holder, view.Read.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	check := func(what string) {
		t.Helper()
		_, body := f.get(t, path)
		after := items(parse(t, body))
		for _, item := range before {
			if !slices.Contains(after, item) {
				t.Errorf("after %s, %s changed or went:\n%v", what, item, after)
			}
		}
	}

	f.advance(time.Hour)
	adjust(schedule.Repace{Pacing: schedule.RRulePacing{RRule: "FREQ=WEEKLY;BYDAY=MO;BYHOUR=9;BYMINUTE=0;BYSECOND=0", TimeZone: "Europe/Dublin", PerRelease: 2}})
	check("a schedule change")
	f.advance(time.Hour)
	adjust(schedule.Skip{Count: 2})
	check("a skip")
	f.advance(time.Hour)
	adjust(schedule.Pause{})
	adjust(schedule.Resume{})
	check("a pause")
}

func TestNotModified(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	view := f.read(t, s)
	path := "/f/" + f.token(t, view.Read.ID, feed.RoleViewer) + ".atom"

	first, _ := f.get(t, path)
	etag := first.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag")
	}
	second, body := f.get(t, path, "If-None-Match", etag)
	if second.StatusCode != http.StatusNotModified || body != "" {
		t.Errorf("status = %d, body %q", second.StatusCode, body)
	}
	if second.Header.Get("ETag") != etag || second.Header.Get("Cache-Control") == "" {
		t.Errorf("headers = %v", second.Header)
	}
	if weak, _ := f.get(t, path, "If-None-Match", `"other", W/`+etag); weak.StatusCode != http.StatusNotModified {
		t.Errorf("a list with a weak match: %d", weak.StatusCode)
	}

	if _, err := f.reads.Adjust(t.Context(), store.Holder, view.Read.ID, schedule.Skip{Count: 1}); err != nil {
		t.Fatal(err)
	}
	third, _ := f.get(t, path, "If-None-Match", etag)
	if third.StatusCode != http.StatusOK || third.Header.Get("ETag") == etag {
		t.Errorf("after a release: %d, %s", third.StatusCode, third.Header.Get("ETag"))
	}

	f.advance(48 * time.Hour)
	tag := third.Header.Get("ETag")
	fourth, _ := f.get(t, path, "If-None-Match", tag)
	if fourth.StatusCode != http.StatusOK {
		t.Errorf("after time passed and more was released: %d", fourth.StatusCode)
	}
}

func TestAnETagDependsOnRoleAndFormat(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	read := f.read(t, s).Read.ID
	viewer, owner := f.token(t, read, feed.RoleViewer), f.token(t, read, feed.RoleOwner)
	a, _ := f.get(t, "/f/"+viewer+".atom")
	b, _ := f.get(t, "/f/"+viewer+".rss")
	c, _ := f.get(t, "/f/"+owner+".atom")
	if a.Header.Get("ETag") == b.Header.Get("ETag") || a.Header.Get("ETag") == c.Header.Get("ETag") {
		t.Errorf("ETags collide: %s %s %s", a.Header.Get("ETag"), b.Header.Get("ETag"), c.Header.Get("ETag"))
	}
}

func TestCacheControl(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		next *time.Time
		want string
	}{
		{nil, "public, max-age=3600"},
		{new(now.Add(10 * time.Minute)), "public, max-age=600"},
		{new(now.Add(time.Second)), "public, max-age=60"},
		{new(now.Add(-time.Minute)), "public, max-age=60"},
		{new(now.Add(24 * time.Hour)), "public, max-age=3600"},
	}
	for _, tt := range tests {
		if got := cacheControl(tt.next, now); got != tt.want {
			t.Errorf("cacheControl(%v) = %q, want %q", tt.next, got, tt.want)
		}
	}
}

func TestNotFound(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	read := f.read(t, s).Read.ID
	viewer, revoked := f.token(t, read, feed.RoleViewer), f.token(t, read, feed.RoleOwner)
	tokens, err := f.store.ListFeedTokens(t.Context(), store.Holder, read, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.RevokeFeedToken(t.Context(), store.Holder, tokens[1].ID); err != nil {
		t.Fatal(err)
	}
	if resp, _ := f.get(t, "/f/"+revoked+".atom"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("revoked token: %d", resp.StatusCode)
	}
	for _, path := range []string{
		"/f/unknown.atom", "/f/" + viewer, "/f/" + viewer + ".xml", "/f/" + viewer + ".", "/", "/f/",
		"/f/" + revoked + "/next", "/f/unknown/next", "/f/" + viewer + "/next",
	} {
		if resp, _ := f.get(t, path); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestRateLimit(t *testing.T) {
	f := newFeedFixture(t, 2)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	read := f.read(t, s).Read.ID
	token, other := f.token(t, read, feed.RoleViewer), f.token(t, read, feed.RoleViewer)
	path := "/f/" + token + ".atom"

	for range 2 {
		if resp, _ := f.get(t, path); resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	}
	resp, _ := f.get(t, path)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	wait, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || wait <= 0 || wait > 1800 {
		t.Errorf("Retry-After = %q", resp.Header.Get("Retry-After"))
	}
	if resp, _ := f.get(t, "/f/"+other+".atom"); resp.StatusCode != http.StatusOK {
		t.Errorf("another token was limited too: %d", resp.StatusCode)
	}

	f.advance(time.Duration(wait) * time.Second)
	if resp, _ := f.get(t, path); resp.StatusCode != http.StatusOK {
		t.Errorf("after the wait: %d", resp.StatusCode)
	}
}

func TestLastSeenIsThrottled(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	read := f.read(t, s).Read.ID
	path := "/f/" + f.token(t, read, feed.RoleViewer) + ".atom"
	seen := func() string {
		t.Helper()
		var v string
		if err := f.store.Pool().QueryRow(t.Context(), "SELECT last_seen_at::text FROM feed_tokens").Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	f.get(t, path)
	first := seen()
	f.get(t, path)
	if got := seen(); got != first {
		t.Errorf("last_seen_at moved within the hour: %s, %s", first, got)
	}
	if _, err := f.store.Pool().Exec(t.Context(), "UPDATE feed_tokens SET last_seen_at = now() - interval '2 hours'"); err != nil {
		t.Fatal(err)
	}
	stale := seen()
	f.get(t, path)
	if seen() == stale {
		t.Error("last_seen_at did not move after an hour")
	}
}

func noRedirects() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

func (f *feedFixture) confirm(t *testing.T, path string) string {
	t.Helper()
	resp, body := f.get(t, path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("confirmation page: %d %s", resp.StatusCode, body)
	}
	match := csrfField.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no CSRF field in %s", body)
	}
	return match[1]
}

func (f *feedFixture) post(t *testing.T, path, csrf string) *http.Response {
	t.Helper()
	resp, err := noRedirects().PostForm(f.server.URL+path, url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp
}

func TestNextOneNow(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	view := f.read(t, s)
	path := "/f/" + f.token(t, view.Read.ID, feed.RoleOwner) + "/next"
	position := func() int {
		t.Helper()
		v, err := f.reads.Get(t.Context(), store.Holder, view.Read.ID)
		if err != nil {
			t.Fatal(err)
		}
		return v.Position
	}

	csrf := f.confirm(t, path)
	// A feed reader or link previewer fetching the link must change nothing.
	if got := position(); got != view.Position {
		t.Fatalf("the GET released something: %d", got)
	}

	resp := f.post(t, path, csrf)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("Location"), fmt.Sprintf("https://publisher.example/%d", view.Position+1); got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if got := position(); got != view.Position+1 {
		t.Errorf("position = %d", got)
	}
	segments, err := f.store.Segments(t.Context(), view.Read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if last := segments[len(segments)-1]; last.Reason != string(schedule.ReasonSkip) {
		t.Errorf("reason = %q", last.Reason)
	}
}

func TestTheTokenDoesNotLeakToThePublisher(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	path := "/f/" + f.token(t, f.read(t, s).Read.ID, feed.RoleOwner) + "/next"

	page, _ := f.get(t, path)
	redirect := f.post(t, path, f.confirm(t, path))
	for name, resp := range map[string]*http.Response{"page": page, "redirect": redirect} {
		if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
			t.Errorf("%s: Referrer-Policy = %q", name, got)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s: Cache-Control = %q", name, got)
		}
	}
}

func TestNextPageHeaders(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 3, "")
	path := "/f/" + f.token(t, f.read(t, s).Read.ID, feed.RoleOwner) + "/next"
	resp, _ := f.get(t, path)
	policy := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(policy, "frame-ancestors 'none'") {
		t.Errorf("policy = %q", policy)
	}
	// form-action would stop the redirect to the publisher's site.
	if strings.Contains(policy, "form-action") {
		t.Errorf("policy = %q", policy)
	}
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
}

func TestNextRefusals(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 4, "")
	view := f.read(t, s)
	raw := f.token(t, view.Read.ID, feed.RoleOwner)
	path := "/f/" + raw + "/next"

	if resp := f.post(t, path, "wrong"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("bad CSRF: %d", resp.StatusCode)
	}
	viewer := "/f/" + f.token(t, view.Read.ID, feed.RoleViewer) + "/next"
	if resp := f.post(t, viewer, "anything"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("a viewer: %d", resp.StatusCode)
	}

	stale := f.confirm(t, path)
	f.advance(25 * time.Minute)
	if resp := f.post(t, path, stale); resp.StatusCode != http.StatusForbidden {
		t.Errorf("an old page: %d", resp.StatusCode)
	}
	recent := f.confirm(t, path)
	f.advance(9 * time.Minute)
	if resp := f.post(t, path, recent); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a recent page: %d", resp.StatusCode)
	}

	if resp := f.post(t, path, f.confirm(t, path)); resp.StatusCode != http.StatusConflict {
		t.Errorf("nothing left: %d", resp.StatusCode)
	}
}

func TestNextWithNoPageGoesHome(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "episode")
	f.entries(t, s, 3, "")
	if _, err := f.store.Pool().Exec(t.Context(), "UPDATE entries SET url = NULL, guid = 'g' || seq WHERE seq = 2"); err != nil {
		t.Fatal(err)
	}
	v, err := f.reads.Create(t.Context(), reads.CreateParams{
		UserID: f.actor.UserID, SourceID: s.ID, Name: "r", Initial: 1, Pacing: schedule.RRulePacing{RRule: dailyAt7, TimeZone: "UTC", PerRelease: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.reads.Adjust(t.Context(), store.Holder, v.Read.ID, schedule.Skip{Count: 1}); err != nil {
		t.Fatal(err)
	}
	path := "/f/" + f.token(t, v.Read.ID, feed.RoleOwner) + "/next"
	resp := f.post(t, path, f.confirm(t, path))
	if got := resp.Header.Get("Location"); got != s.HomeUrl {
		t.Errorf("Location = %q, want the home page", got)
	}
}

func TestRedirectsStayOnTheWeb(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://a.example/": true, "http://a.example/": true, "javascript:alert(1)": false, "file:///etc/passwd": false, "::": false,
	} {
		if got := webURL(raw); got != want {
			t.Errorf("webURL(%q) = %v", raw, got)
		}
	}
}

func TestEpisodesAndArticlesThroughTheDatabase(t *testing.T) {
	f := newFeedFixture(t, 1000)
	podcast := f.source(t, "episode")
	f.entries(t, podcast, 3, "")
	_, err := f.store.Pool().Exec(t.Context(), `UPDATE entries SET duration = '30 minutes',
		enclosures = '[{"url":"https://cdn.publisher.example/e.mp3","type":"audio/mpeg","length":100}]'`)
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, f.read(t, podcast).Read.ID, feed.RoleViewer)
	resp, body := f.get(t, "/f/"+token+".rss")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	item := parse(t, body).Items[0]
	if len(item.Enclosures) != 1 || item.Enclosures[0].Length != "100" {
		t.Errorf("enclosures = %+v", item.Enclosures)
	}
	if item.ITunesExt == nil || item.ITunesExt.Duration != "1800" {
		t.Errorf("itunes = %+v", item.ITunesExt)
	}
}

func TestReadConfigSetsTheWindow(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	view := f.read(t, s)
	config, err := json.Marshal(map[string]int{"window": 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.reads.Update(t.Context(), store.Holder, view.Read.ID, reads.PatchParams{Config: config}); err != nil {
		t.Fatal(err)
	}
	_, body := f.get(t, "/f/"+f.token(t, view.Read.ID, feed.RoleViewer)+".atom")
	if got := len(parse(t, body).Items); got != 2 {
		t.Errorf("items = %d, want 2", got)
	}
}

func TestStartSeqBoundsTheFeed(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 10, "")
	start := 5
	v, err := f.reads.Create(t.Context(), reads.CreateParams{
		UserID: f.actor.UserID, SourceID: s.ID, Name: "r", Initial: 2, StartSeq: &start,
		Pacing: schedule.RRulePacing{RRule: dailyAt7, TimeZone: "UTC", PerRelease: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, body := f.get(t, "/f/"+f.token(t, v.Read.ID, feed.RoleViewer)+".atom")
	got := parse(t, body).Items
	if len(got) != 2 || got[1].Title != "Entry 5" {
		t.Errorf("items = %v", items(parse(t, body)))
	}
}

func TestAFeedWithNothingReleasedYet(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	v := f.read(t, s)
	resp, body := f.get(t, "/f/"+f.token(t, v.Read.ID, feed.RoleViewer)+".rss")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := len(parse(t, body).Items); got != 0 {
		t.Errorf("items = %d", got)
	}
}

func TestMetricsAndLogs(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	token := f.token(t, f.read(t, s).Read.ID, feed.RoleViewer)
	f.get(t, "/f/"+token+".atom")
	f.get(t, "/f/nope.atom")
	f.get(t, "/f/"+token+".xml")

	for _, name := range []string{"andante_feed_requests_total", "andante_feed_render_duration_seconds"} {
		count, err := testutil.GatherAndCount(f.registry, name)
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Errorf("%s has no series", name)
		}
	}
	families, err := f.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if strings.Contains(label.GetValue(), token) {
					t.Errorf("a token in a label of %s", family.GetName())
				}
			}
		}
	}
}

func TestFailuresDoNotExposeTheToken(t *testing.T) {
	f := newFeedFixture(t, 1000)
	s := f.source(t, "page")
	f.entries(t, s, 5, "")
	token := f.token(t, f.read(t, s).Read.ID, feed.RoleViewer)
	f.store.Pool().Close()
	resp, body := f.get(t, "/f/"+token+".atom")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if strings.Contains(body, token) || strings.Contains(f.logs.String(), token) {
		t.Error("the token reached the response or the logs")
	}
}

func TestMatches(t *testing.T) {
	const etag = `"abc"`
	for header, want := range map[string]bool{
		`"abc"`: true, `W/"abc"`: true, `"x", "abc"`: true, "*": true, `"x"`: false, "": false,
	} {
		if got := matches(header, etag); got != want {
			t.Errorf("matches(%q) = %v", header, got)
		}
	}
}

// A podcast from feed to feed: the crawl keeps its metadata, the read's own guids
// replace the publisher's, and replay at 7x turns a weekly show into a daily one.
func TestPodcastFromFeedToFeed(t *testing.T) {
	f := newFeedFixture(t, 1000)
	site := sitetest.Serve(t, "podcast")
	stack := stacktest.NewWith(t, stacktest.Options{Store: f.store})
	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: site.URL + "/feed.xml"}); err != nil {
		t.Fatal(err)
	}
	source, err := stack.Client.CreateSource(t.Context(), replica.SourceCreate{SourceCreate: client.SourceCreate{
		Slug: "podcast", Title: "A Podcast", HomeURL: "https://podcast.example/", DefaultKind: client.Episode, Config: config,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := stack.Sync.CatchUp(t.Context()); err != nil {
		t.Fatal(err)
	}
	stack.Settle(t)

	f.advance(time.Minute) // Nothing releases before it was discovered.
	view, err := f.reads.Create(t.Context(), reads.CreateParams{
		UserID: f.actor.UserID, SourceID: source.ID, Name: "Quick", Initial: 1,
		Pacing: schedule.ReplayPacing{Speed: 7, MinGap: time.Hour, MaxGap: 48 * time.Hour, DefaultGap: 24 * time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	token := f.token(t, view.Read.ID, feed.RoleViewer)
	count := func() *gofeed.Feed {
		_, body := f.get(t, "/f/"+token+".rss")
		return parse(t, body)
	}

	if got := count().Items; len(got) != 1 {
		t.Fatalf("items at the start = %d, want 1", len(got))
	}
	f.advance(23 * time.Hour)
	if got := count().Items; len(got) != 1 {
		t.Errorf("items before a day has passed = %d, want 1", len(got))
	}
	for day := 2; day <= 5; day++ {
		f.advance(24 * time.Hour)
		if got := count().Items; len(got) != day {
			t.Errorf("items near day %d = %d", day, len(got))
		}
	}

	items := count().Items
	item := items[0]
	if item.Title != "Episode 5" {
		t.Fatalf("newest item = %q", item.Title)
	}
	if strings.Contains(item.GUID, "episode-5-guid") {
		t.Errorf("guid = %q: the publisher's guid leaked", item.GUID)
	}
	if len(item.Enclosures) != 1 || item.Enclosures[0].URL != "https://cdn.podcast.example/5.mp3" || item.Enclosures[0].Type != "audio/mpeg" {
		t.Errorf("enclosures = %+v", item.Enclosures)
	}
	if item.ITunesExt == nil || item.ITunesExt.Duration != "3000" || item.ITunesExt.Image != "https://cdn.podcast.example/5.jpg" {
		t.Errorf("itunes = %+v", item.ITunesExt)
	}
}
