// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	cdb "github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/pkg/client"
)

func TestLogs(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	server, cat := stacktest.Logger(f.stack.Logs), stacktest.Logger(f.stack.CatalogueLogs)
	server.Info("feed request", "read", 3, "status", 200)
	server.Info("feed request", "read", 4, "status", 200)
	server.Warn("feed request", "read", 3, "status", 429)
	cat.Info("indexed", "source", "xkcd")
	cat.Error("source stopped", "source", "other")

	bob := f.user(t, "bob", false)
	refused, err := bob.client.ListLogsWithResponse(ctx, nil)
	if err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading them: %v %d", err, refused.StatusCode())
	}

	messages := func(params *client.ListLogsParams) (string, *client.LogList) {
		t.Helper()
		resp, err := f.client.ListLogsWithResponse(ctx, params)
		if err != nil || resp.JSON200 == nil {
			t.Fatalf("list logs: %v %s", err, resp.Body)
		}
		var out []string
		for _, r := range resp.JSON200.Items {
			out = append(out, r.Message+" "+r.Attrs["read"]+r.Attrs["source"]+" "+string(r.Level))
		}
		return strings.Join(out, ","), resp.JSON200
	}

	read, limit := int64(3), int32(1)
	got, page := messages(&client.ListLogsParams{Read: &read, Limit: &limit})
	if got != "feed request 3 warn" || page.Next.IsNull() {
		t.Fatalf("read 3's first page = %q, next %v", got, page.Next)
	}
	// The next link keeps the filter.
	next, err := url.Parse(page.Next.MustGet())
	if err != nil {
		t.Fatal(err)
	}
	if next.Query().Get("read") != "3" {
		t.Errorf("next = %s", next)
	}
	cursor := next.Query().Get("cursor")
	if got, page := messages(&client.ListLogsParams{Read: &read, Limit: &limit, Cursor: &cursor}); got != "feed request 3 info" || !page.Next.IsNull() {
		t.Errorf("read 3's second page = %q, next %v", got, page.Next)
	}

	service, level := client.Catalogue, client.LogLevelError
	if got, _ := messages(&client.ListLogsParams{Service: &service}); got != "source stopped other error,indexed xkcd info" {
		t.Errorf("catalogue = %q", got)
	}
	if got, _ := messages(&client.ListLogsParams{Service: &service, Level: &level}); got != "source stopped other error" {
		t.Errorf("catalogue errors = %q", got)
	}
	source := "xkcd"
	if got, _ := messages(&client.ListLogsParams{Service: &service, Source: &source}); got != "indexed xkcd info" {
		t.Errorf("catalogue for xkcd = %q", got)
	}

	refused, err = f.client.ListLogsWithResponse(ctx, &client.ListLogsParams{Service: &service, Read: &read})
	if err != nil || refused.StatusCode() != http.StatusBadRequest {
		t.Errorf("the catalogue's log by read: %v %d", err, refused.StatusCode())
	}
}

func TestFetches(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	source := f.createSource(t, "xkcd")
	ok := int32(200)
	for _, page := range []string{"1", "2", "3"} {
		if err := f.stack.Catalogue.RecordFetch(ctx, cdb.InsertCrawlLogParams{SourceID: source.ID, Url: ptr("https://comic.example/" + page), Status: &ok}); err != nil {
			t.Fatal(err)
		}
	}

	bob := f.user(t, "bob", false)
	refused, err := bob.client.ListFetchesWithResponse(ctx, source.ID, nil)
	if err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading them: %v %d", err, refused.StatusCode())
	}

	limit := int32(2)
	resp, err := f.client.ListFetchesWithResponse(ctx, source.ID, &client.ListFetchesParams{Limit: &limit})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list fetches: %v %s", err, resp.Body)
	}
	if items := resp.JSON200.Items; len(items) != 2 || items[0].URL != "https://comic.example/3" || resp.JSON200.Next.IsNull() || resp.HTTPResponse.Header.Get("Link") == "" {
		t.Fatalf("first page = %+v", resp.JSON200)
	}
	next, err := url.Parse(resp.JSON200.Next.MustGet())
	if err != nil {
		t.Fatal(err)
	}
	cursor := next.Query().Get("cursor")
	resp, err = f.client.ListFetchesWithResponse(ctx, source.ID, &client.ListFetchesParams{Limit: &limit, Cursor: &cursor})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list fetches: %v %s", err, resp.Body)
	}
	if items := resp.JSON200.Items; len(items) != 1 || items[0].URL != "https://comic.example/1" || !resp.JSON200.Next.IsNull() {
		t.Errorf("second page = %+v", resp.JSON200)
	}

	if err := f.stack.Catalogue.RecordEvent(ctx, catalogue.CrawlEvent{
		SourceID: source.ID, Level: "warn", Message: "no next link", URL: "https://comic.example/3", Page: []byte("<p>3</p>"),
	}); err != nil {
		t.Fatal(err)
	}
	resp, err = f.client.ListFetchesWithResponse(ctx, source.ID, nil)
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Items) != 3 {
		t.Errorf("fetches with an event in the log: %v %s", err, resp.Body)
	}

	missing, err := f.client.ListFetchesWithResponse(ctx, source.ID+1000, nil)
	if err != nil || missing.StatusCode() != http.StatusNotFound {
		t.Errorf("unknown source: %v %d", err, missing.StatusCode())
	}
}

func TestCrawlLog(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	source := f.createSource(t, "xkcd")
	ok := int32(200)
	if err := f.stack.Catalogue.RecordFetch(ctx, cdb.InsertCrawlLogParams{SourceID: source.ID, Url: ptr("https://comic.example/3"), Status: &ok}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []catalogue.CrawlEvent{
		{SourceID: source.ID, Level: "warn", Message: "no next link", URL: "https://comic.example/3", Page: []byte("<p>3</p>")},
		{SourceID: source.ID, Level: "info", Message: "indexed 3 entries"},
	} {
		if err := f.stack.Catalogue.RecordEvent(ctx, e); err != nil {
			t.Fatal(err)
		}
	}

	bob := f.user(t, "bob", false)
	if refused, err := bob.client.ListCrawlLogWithResponse(ctx, source.ID, nil); err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading it: %v %d", err, refused.StatusCode())
	}

	limit := int32(2)
	resp, err := f.client.ListCrawlLogWithResponse(ctx, source.ID, &client.ListCrawlLogParams{Limit: &limit})
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("list crawl log: %v %s", err, resp.Body)
	}
	items := resp.JSON200.Items
	if len(items) != 2 || items[0].Message.MustGet() != "indexed 3 entries" || items[0].HasPage || !items[1].HasPage || resp.JSON200.Next.IsNull() {
		t.Fatalf("first page = %+v", resp.JSON200)
	}
	next, err := url.Parse(resp.JSON200.Next.MustGet())
	if err != nil {
		t.Fatal(err)
	}
	cursor := next.Query().Get("cursor")
	resp, err = f.client.ListCrawlLogWithResponse(ctx, source.ID, &client.ListCrawlLogParams{Limit: &limit, Cursor: &cursor})
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Items) != 1 || resp.JSON200.Items[0].Kind != client.CrawlLogKindFetch {
		t.Errorf("second page: %v %s", err, resp.Body)
	}

	kind := client.CrawlLogKindEvent
	resp, err = f.client.ListCrawlLogWithResponse(ctx, source.ID, &client.ListCrawlLogParams{Kind: &kind, Limit: &limit})
	if err != nil || resp.JSON200 == nil || len(resp.JSON200.Items) != 2 || !resp.JSON200.Next.IsNull() {
		t.Errorf("only events: %v %s", err, resp.Body)
	}

	if refused, err := bob.client.GetCrawledPageWithResponse(ctx, source.ID, items[1].ID); err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading the page: %v %d", err, refused.StatusCode())
	}
	page, err := f.client.GetCrawledPageWithResponse(ctx, source.ID, items[1].ID)
	if err != nil || page.JSON200 == nil || page.JSON200.Body != "<p>3</p>" || page.JSON200.URL != "https://comic.example/3" {
		t.Errorf("crawled page: %v %s", err, page.Body)
	}
	if missing, err := f.client.GetCrawledPageWithResponse(ctx, source.ID, items[0].ID); err != nil || missing.StatusCode() != http.StatusNotFound {
		t.Errorf("an event with no page: %v %d", err, missing.StatusCode())
	}
}
