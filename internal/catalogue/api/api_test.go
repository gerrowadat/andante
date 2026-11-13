// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	catalogueapi "github.com/gerrowadat/andante/internal/catalogue/api"
	"github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
)

type fixture struct {
	store  *store.Store
	client *client.ClientWithResponses
	url    string
	logs   *logring.Ring
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := storetest.Catalogue(t)
	logs := logring.New(10, prometheus.NewRegistry())
	mux := http.NewServeMux()
	mux.Handle(catalogueapi.BasePath+"/", stacktest.CatalogueHandler(t, st, prometheus.NewRegistry(), logs))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := client.NewClientWithResponses(srv.URL+catalogueapi.BasePath, client.WithRequestEditorFn(
		func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer "+stacktest.CatalogueToken)
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{store: st, client: c, url: srv.URL + catalogueapi.BasePath, logs: logs}
}

func feedConfig(t *testing.T, url string) client.SourceConfig {
	t.Helper()
	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: url}); err != nil {
		t.Fatal(err)
	}
	return config
}

func (f *fixture) source(t *testing.T, slug string) client.Source {
	t.Helper()
	resp, err := f.client.CreateSourceWithResponse(t.Context(), client.SourceCreate{
		Slug: slug, Title: "t", HomeURL: "https://a.example/", DefaultKind: client.Page, Config: feedConfig(t, "https://a.example/rss"),
	})
	if err != nil || resp.JSON201 == nil {
		t.Fatalf("create source: %v %s", err, resp.Body)
	}
	return *resp.JSON201
}

func TestEveryOperationNeedsTheToken(t *testing.T) {
	f := newFixture(t)
	for _, token := range []string{"", "wrong"} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.url+"/changes", nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q: status %d", token, resp.StatusCode)
		}
	}
}

func TestSources(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.source(t, "a")
	if s.State != client.SourceStateIndexing {
		t.Errorf("state = %q", s.State)
	}

	paused := client.SourceCreateStatePaused
	created, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "b", Title: "t", HomeURL: "https://a.example/", DefaultKind: client.Article, Config: feedConfig(t, "https://b.example/rss"), State: &paused,
	})
	if err != nil || created.JSON201 == nil || created.JSON201.State != client.SourceStatePaused {
		t.Fatalf("create paused: %v %s", err, created.Body)
	}

	dup, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "a", Title: "t", HomeURL: "https://a.example/", DefaultKind: client.Page, Config: feedConfig(t, "https://a.example/rss"),
	})
	if err != nil || dup.StatusCode() != http.StatusConflict {
		t.Errorf("duplicate slug: %v %d", err, dup.StatusCode())
	}

	bad, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "c", Title: "t", HomeURL: "https://a.example/", DefaultKind: client.Page, Config: feedConfig(t, "ftp://a.example/rss"),
	})
	if err != nil || bad.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("bad config: %v %d", err, bad.StatusCode())
	}

	title := "New"
	patched, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, s.ID, client.SourcePatch{Title: &title})
	if err != nil || patched.JSON200 == nil || patched.JSON200.Title != "New" {
		t.Errorf("patch: %v %s", err, patched.Body)
	}
	missing, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, 999, client.SourcePatch{Title: &title})
	if err != nil || missing.StatusCode() != http.StatusNotFound {
		t.Errorf("patch a missing source: %v %d", err, missing.StatusCode())
	}
}

func TestDemandAndDeletion(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.source(t, "a")

	set, err := f.client.SetDemandWithResponse(ctx, s.ID, client.Demand{Seq: 7})
	if err != nil || set.StatusCode() != http.StatusNoContent {
		t.Fatalf("set demand: %v %d", err, set.StatusCode())
	}
	demand, err := f.store.Demand(ctx, s.ID)
	if err != nil || demand == nil || *demand != 7 {
		t.Errorf("demand = %v, %v", demand, err)
	}
	refused, err := f.client.DeleteSourceWithResponse(ctx, s.ID)
	if err != nil || refused.StatusCode() != http.StatusConflict {
		t.Errorf("delete with demand: %v %d", err, refused.StatusCode())
	}
	cleared, err := f.client.ClearDemandWithResponse(ctx, s.ID)
	if err != nil || cleared.StatusCode() != http.StatusNoContent {
		t.Fatalf("clear demand: %v %d", err, cleared.StatusCode())
	}
	deleted, err := f.client.DeleteSourceWithResponse(ctx, s.ID)
	if err != nil || deleted.StatusCode() != http.StatusNoContent {
		t.Errorf("delete: %v %d", err, deleted.StatusCode())
	}

	for name, status := range map[string]func() (int, error){
		"delete": func() (int, error) { r, err := f.client.DeleteSourceWithResponse(ctx, 999); return r.StatusCode(), err },
		"crawl":  func() (int, error) { r, err := f.client.RequestCrawlWithResponse(ctx, 999); return r.StatusCode(), err },
		"demand": func() (int, error) {
			r, err := f.client.SetDemandWithResponse(ctx, 999, client.Demand{})
			return r.StatusCode(), err
		},
		"clear": func() (int, error) { r, err := f.client.ClearDemandWithResponse(ctx, 999); return r.StatusCode(), err },
	} {
		if got, err := status(); err != nil || got != http.StatusNotFound {
			t.Errorf("%s on a missing source: %v %d", name, err, got)
		}
	}
}

func TestCrawlRequestMakesASourceDue(t *testing.T) {
	f := newFixture(t)
	s := f.source(t, "a")
	resp, err := f.client.RequestCrawlWithResponse(t.Context(), s.ID)
	if err != nil || resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("request crawl: %v %d", err, resp.StatusCode())
	}
	got, err := f.store.GetSource(t.Context(), s.ID)
	if err != nil || got.DueAt == nil {
		t.Errorf("due_at = %v, %v", got.DueAt, err)
	}
}

func TestChanges(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.source(t, "a")
	url, title := "https://a.example/1", "One"
	err := f.store.SaveBatch(ctx, []store.NewEntry{{
		Kind: "page", URL: &url, Title: &title, Images: []string{"https://a.example/1.png"},
		Enclosures: []byte("[]"), Extra: []byte("{}"), Duration: 90 * time.Second, ContentHash: []byte("1"),
	}}, nil, store.Outcome{SourceID: s.ID, Cursor: []byte("{}"), State: "tail"})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := f.client.ListChangesWithResponse(ctx, nil)
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("changes: %v %s", err, resp.Body)
	}
	changes := resp.JSON200.Changes
	if len(changes) != 2 || changes[0].Entry == nil || changes[1].Source == nil {
		t.Fatalf("changes = %s", resp.Body)
	}
	if e := changes[0].Entry; e.SourceID == nil || *e.SourceID != s.ID || e.Images[0] != "https://a.example/1.png" {
		t.Errorf("entry = %+v", e)
	}
	if changes[1].Source.State != client.SourceStateTail {
		t.Errorf("source = %+v", changes[1].Source)
	}

	over := int32(1001)
	tooMany, err := f.client.ListChangesWithResponse(ctx, &client.ListChangesParams{Limit: &over})
	if err != nil || tooMany.StatusCode() != http.StatusBadRequest {
		t.Errorf("limit too high: %v %d", err, tooMany.StatusCode())
	}
}

func TestHeldRequestAnswersWhenSomethingChanges(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.source(t, "a")
	first, err := f.client.ListChangesWithResponse(ctx, nil)
	if err != nil || first.JSON200 == nil {
		t.Fatal(err)
	}
	after := first.JSON200.Changes[len(first.JSON200.Changes)-1].Revision

	wait := int32(30)
	answered := make(chan *client.ListChangesResponse, 1)
	go func() {
		resp, err := f.client.ListChangesWithResponse(ctx, &client.ListChangesParams{After: &after, Wait: &wait})
		if err != nil {
			t.Error(err)
		}
		answered <- resp
	}()

	time.Sleep(300 * time.Millisecond)
	select {
	case <-answered:
		t.Fatal("answered with nothing to say")
	default:
	}
	_, err = f.store.UpdateSource(ctx, s.ID, func(p *db.UpdateSourceParams) error {
		p.Title = "New"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case resp := <-answered:
		if resp.JSON200 == nil || len(resp.JSON200.Changes) != 1 || resp.JSON200.Changes[0].Source.Title != "New" {
			t.Errorf("held answer = %s", resp.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still held after the change")
	}

	one := int32(1)
	start := time.Now()
	empty, err := f.client.ListChangesWithResponse(ctx, &client.ListChangesParams{After: &[]int64{1 << 40}[0], Wait: &one})
	if err != nil || empty.JSON200 == nil || len(empty.JSON200.Changes) != 0 {
		t.Fatalf("empty: %v %s", err, empty.Body)
	}
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("gave up after %s, want about a second", time.Since(start))
	}
}

func TestProbe(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "podcast")
	limit := int32(2)
	resp, err := f.client.CreateProbeWithResponse(t.Context(), client.ProbeRequest{Config: feedConfig(t, site.URL+"/feed.xml"), Limit: &limit})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("probe: %v %s", err, resp.Body)
	}
	if len(resp.JSON200.Entries) != 2 {
		t.Errorf("entries = %d", len(resp.JSON200.Entries))
	}

	broken, err := f.client.CreateProbeWithResponse(t.Context(), client.ProbeRequest{Config: feedConfig(t, site.URL+"/nothing")})
	if err != nil || broken.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("failed probe: %v %d", err, broken.StatusCode())
	}
}

func TestStoppingLetsGoOfHeldRequests(t *testing.T) {
	st := storetest.Catalogue(t)
	stopping, stop := context.WithCancel(t.Context())
	handler, err := catalogueapi.New(catalogueapi.Options{
		Store: st, Token: "token", Registry: prometheus.NewRegistry(), Logger: slog.New(slog.DiscardHandler), Stopping: stopping,
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle(catalogueapi.BasePath+"/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := client.NewClientWithResponses(srv.URL+catalogueapi.BasePath, client.WithRequestEditorFn(
		func(_ context.Context, r *http.Request) error {
			r.Header.Set("Authorization", "Bearer token")
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}

	wait := int32(30)
	answered := make(chan struct{})
	go func() {
		defer close(answered)
		if _, err := c.ListChangesWithResponse(t.Context(), &client.ListChangesParams{Wait: &wait}); err != nil {
			t.Error(err)
		}
	}()
	time.Sleep(300 * time.Millisecond)
	stop()
	select {
	case <-answered:
	case <-time.After(5 * time.Second):
		t.Fatal("still held after stopping")
	}
}

func TestLogs(t *testing.T) {
	f := newFixture(t)
	log := stacktest.Logger(f.logs)
	log.Debug("looking", "source", "a")
	log.Info("indexed", "source", "a", "entries", 3)
	log.Warn("backing off", "source", "b")
	log.Error("stopped", "source", "a")
	ctx := t.Context()

	resp, err := f.client.ListLogsWithResponse(ctx, nil)
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list logs: %v %s", err, resp.Body)
	}
	// The API's own requests log nothing here, so the four are all there is.
	if got := resp.JSON200.Items; len(got) != 4 || got[0].Message != "stopped" || got[0].Level != client.LogLevelError || got[3].Level != client.LogLevelDebug {
		t.Errorf("items = %+v", got)
	}

	source, level, limit := "a", client.LogLevelInfo, int32(1)
	resp, err = f.client.ListLogsWithResponse(ctx, &client.ListLogsParams{Source: &source, Level: &level, Limit: &limit})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list logs: %v %s", err, resp.Body)
	}
	if got := resp.JSON200; len(got.Items) != 1 || got.Items[0].Message != "stopped" || !got.More {
		t.Errorf("filtered = %+v", got)
	}
	before := resp.JSON200.Items[0].ID
	resp, err = f.client.ListLogsWithResponse(ctx, &client.ListLogsParams{Source: &source, Level: &level, Before: &before})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list logs: %v %s", err, resp.Body)
	}
	if items := resp.JSON200.Items; len(items) != 1 || items[0].Message != "indexed" || items[0].Attrs["entries"] != "3" || resp.JSON200.More {
		t.Errorf("older = %+v", resp.JSON200)
	}
}

func TestCrawlLog(t *testing.T) {
	f := newFixture(t)
	s := f.source(t, "a")
	ctx := t.Context()
	ok, failed := int32(200), "connection refused"
	pages := []string{"https://a.example/1", "https://a.example/2", "https://a.example/3"}
	for _, row := range []db.InsertCrawlLogParams{
		{SourceID: s.ID, Url: &pages[0], Status: &ok},
		{SourceID: s.ID, Url: &pages[1], Error: &failed},
	} {
		if err := f.store.RecordFetch(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.RecordEvent(ctx, store.CrawlEvent{SourceID: s.ID, Level: "warn", Message: "no next link", URL: pages[1], Page: []byte("old\xffpage")}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecordFetch(ctx, db.InsertCrawlLogParams{SourceID: s.ID, Url: &pages[2], Status: &ok}); err != nil {
		t.Fatal(err)
	}

	limit := int32(2)
	resp, err := f.client.ListCrawlLogWithResponse(ctx, s.ID, &client.ListCrawlLogParams{Limit: &limit})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list crawl log: %v %s", err, resp.Body)
	}
	page := resp.JSON200
	if len(page.Items) != 2 || !page.More || page.Items[0].URL.MustGet() != pages[2] || page.Items[0].Kind != client.Fetch {
		t.Fatalf("page = %+v", page)
	}
	event := page.Items[1]
	if event.Kind != client.Event || *event.Level != client.LogLevelWarn || event.Message.MustGet() != "no next link" || !event.HasPage {
		t.Errorf("event = %+v", event)
	}
	before := event.ID
	resp, err = f.client.ListCrawlLogWithResponse(ctx, s.ID, &client.ListCrawlLogParams{Before: &before})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list crawl log: %v %s", err, resp.Body)
	}
	if page := resp.JSON200; len(page.Items) != 2 || page.More || page.Items[0].Error.MustGet() != failed || !page.Items[0].Status.IsNull() {
		t.Errorf("older = %+v", page)
	}

	kind := client.Fetch
	resp, err = f.client.ListCrawlLogWithResponse(ctx, s.ID, &client.ListCrawlLogParams{Kind: &kind})
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Items) != 3 {
		t.Errorf("only fetches: %v %s", err, resp.Body)
	}

	withPage := true
	resp, err = f.client.ListCrawlLogWithResponse(ctx, s.ID, &client.ListCrawlLogParams{HasPage: &withPage})
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Items) != 1 || resp.JSON200.Items[0].ID != event.ID {
		t.Errorf("only the kept page: %v %s", err, resp.Body)
	}

	got, err := f.client.GetCrawledPageWithResponse(ctx, s.ID, event.ID)
	if err != nil || got.JSON200 == nil {
		t.Fatalf("crawled page: %v %s", err, got.Body)
	}
	if got.JSON200.URL != pages[1] || got.JSON200.Body != "old\uFFFDpage" {
		t.Errorf("crawled page = %+v", got.JSON200)
	}

	// A newer page replaces it.
	if err := f.store.RecordEvent(ctx, store.CrawlEvent{SourceID: s.ID, Level: "info", Message: "still no next link", URL: pages[2], Page: []byte("new")}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.client.GetCrawledPageWithResponse(ctx, s.ID, event.ID); err != nil || got.StatusCode() != http.StatusNotFound {
		t.Errorf("the replaced page: %v %d", err, got.StatusCode())
	}
	if got, err := f.client.GetCrawledPageWithResponse(ctx, s.ID, page.Items[0].ID); err != nil || got.StatusCode() != http.StatusNotFound {
		t.Errorf("a fetch's page: %v %d", err, got.StatusCode())
	}

	missing, err := f.client.ListCrawlLogWithResponse(ctx, s.ID+1000, nil)
	if err != nil || missing.StatusCode() != http.StatusNotFound {
		t.Errorf("unknown source: %v %d", err, missing.StatusCode())
	}
}
