// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
)

// event is one Server-Sent Event, or a comment when comment is set.
type event struct {
	name, data string
	comment    bool
}

// stream is an open event stream, read the way a browser would.
type stream struct {
	resp   *http.Response
	events chan event
	cancel context.CancelFunc
}

func (f *fixture) stream(t *testing.T, p person, path string) *stream {
	t.Helper()
	return openStream(t, f.server.URL, f.session, p, path)
}

func openStream(t *testing.T, base, cookie string, p person, path string) *stream {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/html, text/event-stream")
	req.Header.Set("X-Forwarded-For", home)
	req.AddCookie(&http.Cookie{Name: cookie, Value: p.session})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &stream{resp: resp, events: make(chan event, 100), cancel: cancel}
	t.Cleanup(s.close)
	if resp.StatusCode != http.StatusOK {
		return s
	}
	go func() {
		defer close(s.events)
		lines := bufio.NewScanner(resp.Body)
		var e event
		var data []string
		for lines.Scan() {
			line := lines.Text()
			switch {
			case line == "" && (e.name != "" || data != nil):
				e.data = strings.Join(data, "\n")
				s.events <- e
				e, data = event{}, nil
			case strings.HasPrefix(line, ":"):
				s.events <- event{comment: true}
			case strings.HasPrefix(line, "event: "):
				e.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			}
		}
	}()
	return s
}

func (s *stream) close() {
	s.cancel()
	_ = s.resp.Body.Close()
}

// next returns the next event that isn't a comment, and false if the stream ends
// first.
func (s *stream) next(t *testing.T) (event, bool) {
	t.Helper()
	timeout := time.After(30 * time.Second)
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				return event{}, false
			}
			if !e.comment {
				return e, true
			}
		case <-timeout:
			t.Fatal("no event")
		}
	}
}

// until reads events until one contains want, and returns it.
func (s *stream) until(t *testing.T, want string) event {
	t.Helper()
	for {
		e, ok := s.next(t)
		if !ok {
			t.Fatalf("the stream ended before %q", want)
		}
		if strings.Contains(e.data, want) {
			return e
		}
	}
}

// ended says whether the stream ends, without anything but comments, soon.
func (s *stream) ended(t *testing.T) bool {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case e, ok := <-s.events:
			if !ok {
				return true
			}
			if !e.comment {
				t.Errorf("an event after the end: %+v", e)
			}
		case <-timeout:
			return false
		}
	}
}

// The waiting page shows what turns up without a reload. Without JavaScript it
// still refreshes itself.
func TestTheAddPageShowsWhatTurnsUp(t *testing.T) {
	f := newFixture(t)
	alice, mallory := f.person(t, "alice", false), f.person(t, "mallory", false)
	site := sitetest.Serve(t, "podcast")

	started := f.post(t, alice, "/add", url.Values{"url": {site.URL + "/feed.xml"}})
	at := started.header.Get("Location")
	if started.status != http.StatusSeeOther || at == "" {
		t.Fatalf("start: %d %s", started.status, started.body)
	}
	events := f.stream(t, alice, at+"/events")
	if events.resp.StatusCode != http.StatusOK || events.resp.Header.Get("Content-Type") != "text/event-stream" || events.resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("the stream: %d %v", events.resp.StatusCode, events.resp.Header)
	}
	found := events.until(t, "Add this")
	worksWithoutScripts(t, at+"/events", found.data)
	if !strings.Contains(found.data, "Episode 1") || !csrfJob.MatchString(found.data) || !strings.Contains(found.data, alice.csrf) {
		t.Errorf("what turned up: %s", found.data)
	}
	// The steps the catalogue took are kept, folded away.
	if !strings.Contains(found.data, "How andante looked") || !strings.Contains(found.data, "Fetching "+site.URL+"/feed.xml") {
		t.Errorf("the steps aren't there: %s", found.data)
	}
	if done, ok := events.next(t); !ok || done.name != "done" {
		t.Errorf("after the candidates: %+v, %v", done, ok)
	}
	if !events.ended(t) {
		t.Error("the stream carried on after it was done")
	}

	// Once it's done the page has no stream and no refresh. A stream asked for anyway
	// says the same and stops.
	page := f.get(t, alice, at)
	if strings.Contains(page.body, "hx-sse:connect") || strings.Contains(page.body, "refresh") {
		t.Errorf("a finished look still waits: %s", page.body)
	}
	again := f.stream(t, alice, at+"/events")
	again.until(t, "Add this")
	if done, ok := again.next(t); !ok || done.name != "done" {
		t.Errorf("a finished look's stream: %+v, %v", done, ok)
	}
	if got := metric(t, f, "andante_web_stream_events_total", "discovery"); got < 4 {
		t.Errorf("discovery events counted: %v", got)
	}

	// Somebody else's look is a 404, stream and all.
	if theirs := f.stream(t, mallory, at+"/events"); theirs.resp.StatusCode != http.StatusNotFound {
		t.Errorf("mallory's stream of alice's look: %d", theirs.resp.StatusCode)
	}
}

func TestTheWaitingPageRefreshesOnlyWithoutJavaScript(t *testing.T) {
	f := newFixture(t)
	alice := f.person(t, "alice", false)
	// Nothing answers here, so the look takes as long as the catalogue gives it.
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	t.Cleanup(hang.Close)
	t.Cleanup(hang.CloseClientConnections)

	started := f.post(t, alice, "/add", url.Values{"url": {hang.URL + "/"}})
	page := f.get(t, alice, started.header.Get("Location"))
	// A parser with scripting on, as goquery's is, reads <noscript> as text.
	if strings.Count(page.body, "http-equiv") != 1 || !strings.Contains(page.body, `<noscript><meta http-equiv="refresh" content="3"></noscript>`) {
		t.Errorf("the refresh isn't only for a browser without JavaScript: %s", page.body)
	}
	doc := parse(t, page.body)
	if connect, _ := doc.Find("#look").Attr("hx-sse:connect"); connect != started.header.Get("Location")+"/events" {
		t.Errorf("the page follows %q", connect)
	}
	events := f.stream(t, alice, started.header.Get("Location")+"/events")
	if e := events.until(t, "Looking"); e.name != "" {
		t.Errorf("the first event is %+v", e)
	}
	// The catalogue says what it's doing, and the page shows it as it happens.
	busy := events.until(t, "Fetching "+hang.URL+"/")
	if item := parse(t, busy.data).Find(`li[aria-busy="true"]`).Text(); item != "Fetching "+hang.URL+"/" {
		t.Errorf("the step under way is %q", item)
	}
	if page := f.get(t, alice, started.header.Get("Location")); !strings.Contains(page.body, "Fetching "+hang.URL+"/") {
		t.Errorf("the page doesn't show the step: %s", page.body)
	}
}

func (f *fixture) indexMore(t *testing.T, source int64, n int) {
	t.Helper()
	var entries []catalogue.NewEntry
	for i := range n {
		u := "https://comic.example/more/" + strconv.Itoa(i)
		title := "More " + strconv.Itoa(i)
		entries = append(entries, catalogue.NewEntry{Kind: "page", URL: &u, Title: &title, Images: []string{}, Enclosures: []byte("[]"), Extra: []byte("{}"), ContentHash: []byte(u)})
	}
	err := f.stack.Catalogue.SaveBatch(t.Context(), entries, nil, catalogue.Outcome{SourceID: source, Cursor: []byte("{}"), State: "indexing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.stack.Sync.CatchUp(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The source page shows indexing as the sync brings it, with the crawl log for an
// admin, and stops when the source goes.
func TestTheSourcePageShowsIndexingAsItHappens(t *testing.T) {
	f := newFixture(t)
	dave, alice := f.person(t, "dave", true), f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	at := "/sources/" + strconv.FormatInt(source, 10)

	if page := f.get(t, alice, at); !strings.Contains(page.body, `hx-sse:connect="`+at+`/events"`) || !strings.Contains(page.body, "5 so far") {
		t.Errorf("the source page: %s", page.body)
	}
	hers, his := f.stream(t, alice, at+"/events"), f.stream(t, dave, at+"/events")
	first := hers.until(t, "5 so far")
	worksWithoutScripts(t, at+"/events", first.data)
	if !strings.Contains(first.data, "Reached the live end") || !strings.Contains(first.data, `id="first-entries" hx-swap-oob="true"`) {
		t.Errorf("alice's first event: %s", first.data)
	}
	if strings.Contains(first.data, "crawl-log") {
		t.Errorf("alice sees the crawl log: %s", first.data)
	}
	if mine := his.until(t, "5 so far"); !strings.Contains(mine.data, `id="crawl-log" hx-swap-oob="true"`) {
		t.Errorf("an admin's event has no crawl log: %s", mine.data)
	}

	f.indexMore(t, source, 3)
	if got := hers.until(t, "8 so far"); !strings.Contains(got.data, "More 2") || !strings.Contains(got.data, "Indexing") {
		t.Errorf("after more were indexed: %s", got.data)
	}
	his.until(t, "8 so far")

	if err := f.stack.App.DeleteSource(t.Context(), dave.actor, source); err != nil {
		t.Fatal(err)
	}
	if err := f.stack.Sync.CatchUp(t.Context()); err != nil {
		t.Fatal(err)
	}
	hers.until(t, "deleted")
	if gone, ok := hers.next(t); !ok || gone.name != "gone" {
		t.Errorf("after the delete: %+v, %v", gone, ok)
	}
	if missing := f.stream(t, alice, at+"/events"); missing.resp.StatusCode != http.StatusNotFound {
		t.Errorf("the stream of a deleted source: %d", missing.resp.StatusCode)
	}
}

// A stream lasts longer than the server's timeouts, kept alive by comments.
func TestAStreamOutlivesTheServersTimeouts(t *testing.T) {
	f := newFixture(t)
	alice := f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	short := httptest.NewUnstartedServer(f.server.Config.Handler)
	short.Config.ReadTimeout, short.Config.WriteTimeout = 300*time.Millisecond, 300*time.Millisecond
	short.Start()
	t.Cleanup(short.Close)

	at := "/sources/" + strconv.FormatInt(source, 10)
	events := openStream(t, short.URL, f.session, alice, at+"/events")
	events.until(t, "5 so far")
	time.Sleep(time.Second)
	f.indexMore(t, source, 1)
	events.until(t, "6 so far")
	if got := metric(t, f, "andante_web_streams", "source"); got != 1 {
		t.Errorf("open source streams: %v", got)
	}
}

// Each person can only have so many open, and a stream ends when the server stops.
func TestStreamsAreLimitedAndEndWithTheServer(t *testing.T) {
	f := newFixture(t)
	alice, bob := f.person(t, "alice", false), f.person(t, "bob", false)
	source := f.source(t, "Harbour Lights")
	at := "/sources/" + strconv.FormatInt(source, 10) + "/events"
	var open []*stream
	for range 8 {
		s := f.stream(t, alice, at)
		s.until(t, "5 so far")
		open = append(open, s)
	}
	if more := f.stream(t, alice, at); more.resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("one more than the limit: %d", more.resp.StatusCode)
	}
	if got := metric(t, f, "andante_web_streams_refused_total", "source"); got != 1 {
		t.Errorf("refused: %v", got)
	}
	bobs := f.stream(t, bob, at)
	bobs.until(t, "5 so far")

	// Closing one makes room.
	open[0].close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s := f.stream(t, alice, at)
		if s.resp.StatusCode == http.StatusOK {
			s.until(t, "5 so far")
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("closing a stream didn't make room")
		}
		time.Sleep(20 * time.Millisecond)
	}

	close(f.stopping)
	for _, s := range append(open[1:], bobs) {
		if !s.ended(t) {
			t.Error("a stream outlasted the server")
		}
	}
}

// metric reads one stream's value of a metric.
func metric(t *testing.T, f *fixture, name, stream string) float64 {
	t.Helper()
	families, err := f.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			for _, label := range m.GetLabel() {
				if label.GetName() == "stream" && label.GetValue() == stream {
					return m.GetCounter().GetValue() + m.GetGauge().GetValue()
				}
			}
		}
	}
	return 0
}
