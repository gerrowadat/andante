// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/stacktest"
)

func TestTheLogsPage(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	server, cat := stacktest.Logger(f.stack.Logs), stacktest.Logger(f.stack.CatalogueLogs)
	server.Info("feed request", "read", 3, "status", 200)
	server.Warn("feed request", "read", 4, "status", 429)
	cat.Info("indexed", "source", "xkcd", "entries", 5)

	got := f.get(t, dave, "/logs?level=warn")
	if got.status != http.StatusOK || !strings.Contains(got.body, "read=4 status=429") || strings.Contains(got.body, "read=3") {
		t.Errorf("the server's warnings: %d %s", got.status, got.body)
	}
	got = f.get(t, dave, "/logs?read=3")
	if !strings.Contains(got.body, "read=3 status=200") || strings.Contains(got.body, "read=4") {
		t.Errorf("read 3: %s", got.body)
	}
	got = f.get(t, dave, "/logs?service=catalogue&source=xkcd&read=3")
	if !strings.Contains(got.body, "entries=5 source=xkcd") {
		t.Errorf("the catalogue's log of xkcd: %s", got.body)
	}

	f.stack.CatalogueServer.Close()
	got = f.get(t, dave, "/logs?service=catalogue")
	if got.status != http.StatusOK || !strings.Contains(got.body, "The catalogue didn&#39;t answer.") {
		t.Errorf("with the catalogue down: %d %s", got.status, got.body)
	}
}

func TestTheLogsPageGoesBackInPages(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	server := stacktest.Logger(f.stack.Logs)
	for i := range 150 {
		server.Info("numbered", "n", i, "source", "xkcd")
	}
	got := f.get(t, dave, "/logs?source=xkcd")
	if !strings.Contains(got.body, "n=149 ") || strings.Contains(got.body, "n=49 ") {
		t.Fatalf("first page: %s", got.body)
	}
	older := regexp.MustCompile(`href="(/logs\?[^"]+)">Older<`).FindStringSubmatch(got.body)
	if older == nil {
		t.Fatalf("no link to older records: %s", got.body)
	}
	got = f.get(t, dave, html.UnescapeString(older[1]))
	if !strings.Contains(got.body, "n=49 ") || strings.Contains(got.body, "n=50 ") || strings.Contains(got.body, ">Older<") {
		t.Errorf("second page: %s", got.body)
	}
}

func TestAnAdminSeesTheCrawlLog(t *testing.T) {
	f := newFixture(t)
	dave, bob := f.person(t, "dave", true), f.person(t, "bob", false)
	source := f.source(t, "Crawled")
	status, problem := int32(200), "connection refused"
	for _, row := range []db.InsertCrawlLogParams{
		{SourceID: source, Url: ptr("https://comic.example/fine"), Status: &status},
		{SourceID: source, Url: ptr("https://comic.example/broken"), Error: &problem},
	} {
		if err := f.stack.Catalogue.RecordFetch(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	err := f.stack.Catalogue.RecordEvent(t.Context(), catalogue.CrawlEvent{
		SourceID: source, Level: "warn", Message: "no next link on https://comic.example/fine", URL: "https://comic.example/fine",
		Page: []byte(`<script>alert("hi")</script>`),
	})
	if err != nil {
		t.Fatal(err)
	}
	page := "/sources/" + strconv.FormatInt(source, 10)

	got := f.get(t, dave, page)
	for _, want := range []string{
		"Crawl log", "https://comic.example/fine", "connection refused", "/logs?service=catalogue&amp;source=",
		`<tr class="warn">`, "no next link on https://comic.example/fine", "<details>",
		"&lt;script&gt;alert(&#34;hi&#34;)&lt;/script&gt;",
	} {
		if !strings.Contains(got.body, want) {
			t.Errorf("an admin's source page has no %q", want)
		}
	}
	if strings.Contains(got.body, "<script>alert") {
		t.Error("the kept page is not escaped")
	}
	if got := f.get(t, bob, page); strings.Contains(got.body, "Crawl log") {
		t.Error("a user sees the crawl log")
	}

	f.stack.CatalogueServer.Close()
	got = f.get(t, dave, page)
	if got.status != http.StatusOK || !strings.Contains(got.body, "The catalogue didn't answer.") {
		t.Errorf("with the catalogue down: %d", got.status)
	}
}

func TestAnAdminsReadLinksToItsFeedRequests(t *testing.T) {
	f := newFixture(t)
	dave, bob := f.person(t, "dave", true), f.person(t, "bob", false)
	source := f.source(t, "Linked")
	mine, bobs := f.track(t, dave, source), f.track(t, bob, source)
	if got := f.get(t, dave, "/reads/"+strconv.FormatInt(mine, 10)); !strings.Contains(got.body, "/logs?read="+strconv.FormatInt(mine, 10)) {
		t.Error("no link to the read's feed requests")
	}
	if got := f.get(t, bob, "/reads/"+strconv.FormatInt(bobs, 10)); strings.Contains(got.body, "/logs?") {
		t.Error("a user is offered the logs")
	}
}

func ptr[T any](v T) *T { return &v }
