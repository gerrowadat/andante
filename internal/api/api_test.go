// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	specfile "github.com/gerrowadat/andante/api"
	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/oapi-codegen/nullable"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const problemJSON = "application/problem+json"

type fixture struct {
	url      string
	token    string
	store    *store.Store
	client   *client.ClientWithResponses
	registry *prometheus.Registry
}

// newFixture serves the API on a real database, with one API token.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := storetest.New(t)
	reg := prometheus.NewRegistry()
	handler, err := api.New(api.Options{
		Store:      st,
		Strategies: discovery.NewRegistry(strategies.Crawl{}),
		Registry:   reg,
		Logger:     slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	_, token, err := st.CreateAPIToken(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.NewClientWithResponses(srv.URL+api.BasePath, client.WithRequestEditorFn(bearer(token)))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{url: srv.URL, token: token, store: st, client: c, registry: reg}
}

func bearer(token string) client.RequestEditorFn {
	return func(_ context.Context, r *http.Request) error {
		r.Header.Set("Authorization", "Bearer "+token)
		return nil
	}
}

func crawlConfig(t *testing.T, startURL string) client.SourceConfig {
	t.Helper()
	var c client.SourceConfig
	if err := c.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: startURL}); err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) createSource(t *testing.T, slug string) client.Source {
	t.Helper()
	resp, err := f.client.CreateSourceWithResponse(t.Context(), client.SourceCreate{
		Slug:        slug,
		Title:       "Title of " + slug,
		HomeURL:     "https://comic.example/",
		DefaultKind: client.Page,
		Config:      crawlConfig(t, "https://comic.example/1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.JSON201 == nil {
		t.Fatalf("create source: %d %s", resp.StatusCode(), resp.Body)
	}
	return *resp.JSON201
}

func TestSourceLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	created := f.createSource(t, "example")
	if created.State != client.SourceStateIndexing {
		t.Errorf("state = %q, want indexing", created.State)
	}
	strategy, err := created.Config.Discriminator()
	if err != nil || strategy != "crawl" {
		t.Errorf("strategy = %q, %v", strategy, err)
	}

	got, err := f.client.GetSourceWithResponse(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON200 == nil || got.JSON200.Slug != "example" {
		t.Fatalf("get: %d %s", got.StatusCode(), got.Body)
	}
	crawl, err := got.JSON200.Config.AsCrawlConfig()
	if err != nil || crawl.StartURL != "https://comic.example/1" {
		t.Errorf("config = %+v, %v", crawl, err)
	}

	title, state := "New Title", client.SourcePatchStatePaused
	patched, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, created.ID, client.SourcePatch{
		Title:    &title,
		State:    &state,
		ImageURL: nullable.NewNullableWithValue("https://comic.example/logo.png"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if patched.JSON200 == nil {
		t.Fatalf("patch: %d %s", patched.StatusCode(), patched.Body)
	}
	if patched.JSON200.Title != title || patched.JSON200.State != client.SourceStatePaused || patched.JSON200.ImageURL.MustGet() != "https://comic.example/logo.png" {
		t.Errorf("patched = %+v", patched.JSON200)
	}

	cleared, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, created.ID, client.SourcePatch{
		ImageURL: nullable.NewNullNullable[string](),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cleared.JSON200 == nil || !cleared.JSON200.ImageURL.IsNull() {
		t.Fatalf("clearing image_url: %d %s", cleared.StatusCode(), cleared.Body)
	}
	if cleared.JSON200.Title != title {
		t.Errorf("a patch without title changed it to %q", cleared.JSON200.Title)
	}

	config := crawlConfig(t, "https://comic.example/2")
	reconfigured, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, created.ID, client.SourcePatch{Config: &config})
	if err != nil {
		t.Fatal(err)
	}
	if reconfigured.JSON200 == nil {
		t.Fatalf("patching config: %d %s", reconfigured.StatusCode(), reconfigured.Body)
	}

	deleted, err := f.client.DeleteSourceWithResponse(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode() != http.StatusNoContent {
		t.Errorf("delete: %d %s", deleted.StatusCode(), deleted.Body)
	}
	gone, err := f.client.GetSourceWithResponse(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone.StatusCode() != http.StatusNotFound {
		t.Errorf("get after delete: %d", gone.StatusCode())
	}
}

func TestSourceConflicts(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	created := f.createSource(t, "taken")

	dup, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "taken", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page,
		Config: crawlConfig(t, "https://comic.example/1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dup.StatusCode() != http.StatusConflict || dup.ApplicationProblemJSONDefault == nil {
		t.Errorf("duplicate slug: %d %s", dup.StatusCode(), dup.Body)
	}

	_, err = f.store.Pool().Exec(ctx, "INSERT INTO reads (source_id, name) VALUES ($1, 'r')", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := f.client.DeleteSourceWithResponse(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refused.StatusCode() != http.StatusConflict {
		t.Errorf("delete with a read: %d %s", refused.StatusCode(), refused.Body)
	}
}

func TestSourceConfigIsValidatedOnWrite(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	var feed client.SourceConfig
	if err := feed.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: "https://feed.example/rss"}); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]client.SourceConfig{
		"strategy not built yet": feed,
		"bad start URL":          crawlConfig(t, "ftp://comic.example/"),
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
				Slug: "s", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: config,
			})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode() != http.StatusUnprocessableEntity || resp.ApplicationProblemJSONDefault == nil {
				t.Errorf("got %d %s", resp.StatusCode(), resp.Body)
			}
		})
	}

	s := f.createSource(t, "kept")
	bad := crawlConfig(t, "ftp://comic.example/")
	title := "Not Applied"
	resp, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, s.ID, client.SourcePatch{Title: &title, Config: &bad})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("patch with a bad config: %d %s", resp.StatusCode(), resp.Body)
	}
	got, err := f.client.GetSourceWithResponse(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON200.Title == title {
		t.Error("a refused patch still changed the title")
	}
}

func TestPagination(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	want := []string{"a", "b", "c", "d", "e"}
	for _, slug := range want {
		f.createSource(t, slug)
	}

	var got []string
	var cursor *string
	limit := int32(2)
	pages := 0
	for {
		pages++
		resp, err := f.client.ListSourcesWithResponse(ctx, &client.ListSourcesParams{Cursor: cursor, Limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		if resp.JSON200 == nil {
			t.Fatalf("list: %d %s", resp.StatusCode(), resp.Body)
		}
		for _, s := range resp.JSON200.Items {
			got = append(got, s.Slug)
		}
		if resp.JSON200.Next.IsNull() {
			if link := resp.HTTPResponse.Header.Get("Link"); link != "" {
				t.Errorf("last page has a Link header: %q", link)
			}
			break
		}
		next := resp.JSON200.Next.MustGet()
		if want := "<" + next + `>; rel="next"`; resp.HTTPResponse.Header.Get("Link") != want {
			t.Errorf("Link = %q, want %q", resp.HTTPResponse.Header.Get("Link"), want)
		}
		value := regexp.MustCompile(`cursor=([^&]+)`).FindStringSubmatch(next)[1]
		cursor = &value
	}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("slugs = %v, want %v", got, want)
	}
	if pages != 3 {
		t.Errorf("pages = %d, want 3", pages)
	}
}

func TestEntries(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "comic")

	empty, err := f.client.ListEntriesWithResponse(ctx, s.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty.JSON200 == nil || len(empty.JSON200.Items) != 0 {
		t.Fatalf("empty list: %d %s", empty.StatusCode(), empty.Body)
	}

	const insert = `INSERT INTO entries (source_id, seq, kind, url, duration, enclosures)
		VALUES ($1, $2, 'episode', $3, '90 seconds', '[{"url":"https://a.example/1.mp3","type":"audio/mpeg","length":10}]')`
	for _, e := range []struct {
		seq int
		url string
	}{{-1, "https://a.example/before"}, {0, "https://a.example/0"}, {1, "https://a.example/1"}} {
		if _, err := f.store.Pool().Exec(ctx, insert, s.ID, e.seq, e.url); err != nil {
			t.Fatal(err)
		}
	}

	limit := int32(2)
	first, err := f.client.ListEntriesWithResponse(ctx, s.ID, &client.ListEntriesParams{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if first.JSON200 == nil || len(first.JSON200.Items) != 2 || first.JSON200.Items[0].Seq != -1 {
		t.Fatalf("first page: %d %s", first.StatusCode(), first.Body)
	}
	if len(first.JSON200.Items[0].Enclosures) != 1 || first.JSON200.Items[0].Enclosures[0].URL != "https://a.example/1.mp3" {
		t.Errorf("enclosures = %+v", first.JSON200.Items[0].Enclosures)
	}
	if !strings.Contains(string(first.Body), `"duration":"PT1M30S"`) {
		t.Errorf("duration not ISO 8601 in %s", first.Body)
	}

	next := first.JSON200.Next.MustGet()
	cursor := regexp.MustCompile(`cursor=([^&]+)`).FindStringSubmatch(next)[1]
	second, err := f.client.ListEntriesWithResponse(ctx, s.ID, &client.ListEntriesParams{Cursor: &cursor, Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if second.JSON200 == nil || len(second.JSON200.Items) != 1 || second.JSON200.Items[0].Seq != 1 {
		t.Fatalf("second page: %d %s", second.StatusCode(), second.Body)
	}

	missing, err := f.client.ListEntriesWithResponse(ctx, 9999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("entries of a missing source: %d", missing.StatusCode())
	}
}

func TestAPITokens(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()

	created, err := f.client.CreateAPITokenWithResponse(ctx, client.APITokenCreate{Label: "laptop"})
	if err != nil {
		t.Fatal(err)
	}
	if created.JSON201 == nil || created.JSON201.Token == "" {
		t.Fatalf("create: %d %s", created.StatusCode(), created.Body)
	}

	list, err := f.client.ListAPITokensWithResponse(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.JSON200 == nil || len(list.JSON200.Items) != 2 {
		t.Fatalf("list: %d %s", list.StatusCode(), list.Body)
	}
	if strings.Contains(string(list.Body), created.JSON201.Token) || strings.Contains(string(list.Body), `"token"`) {
		t.Errorf("list exposes a raw token: %s", list.Body)
	}

	second, err := client.NewClientWithResponses(f.url+api.BasePath, client.WithRequestEditorFn(bearer(created.JSON201.Token)))
	if err != nil {
		t.Fatal(err)
	}
	if resp, err := second.ListSourcesWithResponse(ctx, nil); err != nil || resp.StatusCode() != http.StatusOK {
		t.Fatalf("new token: %v %v", resp, err)
	}

	revoked, err := f.client.RevokeAPITokenWithResponse(ctx, created.JSON201.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", revoked.StatusCode(), revoked.Body)
	}
	if resp, err := second.ListSourcesWithResponse(ctx, nil); err != nil || resp.StatusCode() != http.StatusUnauthorized {
		t.Errorf("revoked token: %v %v", resp.StatusCode(), err)
	}
	again, err := f.client.RevokeAPITokenWithResponse(ctx, created.JSON201.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.StatusCode() != http.StatusNoContent {
		t.Errorf("revoking twice: %d %s", again.StatusCode(), again.Body)
	}

	missing, err := f.client.RevokeAPITokenWithResponse(ctx, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("revoking a missing token: %d", missing.StatusCode())
	}

	list, err = f.client.ListAPITokensWithResponse(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	last, err := f.client.RevokeAPITokenWithResponse(ctx, list.JSON200.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if last.StatusCode() != http.StatusConflict {
		t.Errorf("revoking the last active token: %d %s", last.StatusCode(), last.Body)
	}
}

func TestLastUsedAtIsThrottled(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	read := func() string {
		t.Helper()
		var v string
		err := f.store.Pool().QueryRow(ctx, "SELECT last_used_at::text FROM api_tokens WHERE label = 'test'").Scan(&v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}

	if _, err := f.client.ListSourcesWithResponse(ctx, nil); err != nil {
		t.Fatal(err)
	}
	first := read()
	if _, err := f.client.ListSourcesWithResponse(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != first {
		t.Errorf("last_used_at moved within a minute: %s, then %s", first, got)
	}

	_, err := f.store.Pool().Exec(ctx, "UPDATE api_tokens SET last_used_at = now() - interval '2 minutes'")
	if err != nil {
		t.Fatal(err)
	}
	stale := read()
	if _, err := f.client.ListSourcesWithResponse(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if got := read(); got == stale {
		t.Error("last_used_at did not move after more than a minute")
	}
}

// do sends a raw request and returns the response with its body read.
func (f *fixture) do(t *testing.T, method, path, contentType, body string, token string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, f.url+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

func TestEveryErrorIsAProblem(t *testing.T) {
	f := newFixture(t)
	s := f.createSource(t, "exists")
	const js = "application/json"
	tests := []struct {
		name                string
		method, path        string
		contentType, body   string
		token               string
		wantStatus          int
		wantType, wantField string
		wantAllow           string
	}{
		{name: "no token", method: "GET", path: "/api/v1/sources", wantStatus: 401, wantType: "unauthorized"},
		{name: "bad token", method: "GET", path: "/api/v1/sources", token: "nope", wantStatus: 401, wantType: "unauthorized"},
		{name: "unknown route", method: "GET", path: "/api/v1/nope", token: f.token, wantStatus: 404},
		{name: "unknown route, no token", method: "GET", path: "/api/v1/nope", wantStatus: 404},
		{name: "wrong method", method: "PUT", path: "/api/v1/sources", token: f.token, wantStatus: 405, wantAllow: "GET, POST"},
		{name: "wrong method on a template", method: "POST", path: "/api/v1/sources/1", token: f.token, wantStatus: 405, wantAllow: "GET, PATCH, DELETE"},
		{name: "missing nested config", method: "POST", path: "/api/v1/probes", contentType: js, body: "{}", token: f.token, wantStatus: 400, wantType: "invalid-request", wantField: "/config"},
		{name: "unknown ID", method: "GET", path: "/api/v1/sources/9999", token: f.token, wantStatus: 404},
		{name: "non-numeric ID", method: "GET", path: "/api/v1/sources/abc", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "limit too high", method: "GET", path: "/api/v1/sources?limit=101", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "bad cursor", method: "GET", path: "/api/v1/sources?cursor=!!", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "cursor of the wrong kind", method: "GET", path: "/api/v1/sources?cursor=bm9wZQ", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "malformed JSON", method: "POST", path: "/api/v1/api-tokens", contentType: js, body: "{", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "missing field", method: "POST", path: "/api/v1/api-tokens", contentType: js, body: "{}", token: f.token, wantStatus: 400, wantType: "invalid-request", wantField: "/label"},
		{name: "unknown field", method: "POST", path: "/api/v1/api-tokens", contentType: js, body: `{"label":"l","bogus":1}`, token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "wrong content type", method: "POST", path: "/api/v1/api-tokens", contentType: "text/plain", body: "x", token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "bad slug", method: "POST", path: "/api/v1/sources", contentType: js, token: f.token, wantStatus: 400, wantType: "invalid-request", wantField: "/slug",
			body: `{"slug":"Not A Slug","title":"t","home_url":"https://e.example/","default_kind":"page","config":{"strategy":"crawl","start_url":"https://e.example/"}}`},
		{name: "unknown strategy", method: "POST", path: "/api/v1/sources", contentType: js, token: f.token, wantStatus: 400, wantType: "invalid-request",
			body: `{"slug":"s","title":"t","home_url":"https://e.example/","default_kind":"page","config":{"strategy":"nope"}}`},
		{name: "slug in a patch", method: "PATCH", path: "/api/v1/sources/" + itoa(s.ID), contentType: "application/merge-patch+json", body: `{"slug":"new"}`, token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "state the crawler owns", method: "PATCH", path: "/api/v1/sources/" + itoa(s.ID), contentType: "application/merge-patch+json", body: `{"state":"tail"}`, token: f.token, wantStatus: 400, wantType: "invalid-request"},
		{name: "not built yet", method: "GET", path: "/api/v1/reads", token: f.token, wantStatus: 501, wantType: "not-implemented"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := f.do(t, tt.method, tt.path, tt.contentType, tt.body, tt.token)
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d: %s", resp.StatusCode, tt.wantStatus, body)
			}
			if got := resp.Header.Get("Content-Type"); got != problemJSON {
				t.Errorf("Content-Type = %q, want %s", got, problemJSON)
			}
			var p client.Problem
			if err := json.Unmarshal([]byte(body), &p); err != nil {
				t.Fatalf("body is not JSON: %s", body)
			}
			if int(p.Status) != tt.wantStatus || p.Title == "" {
				t.Errorf("problem = %+v", p)
			}
			wantType := "about:blank"
			if tt.wantType != "" {
				wantType = "https://github.com/gerrowadat/andante/blob/main/docs/api.md#" + tt.wantType
			}
			if p.Type != wantType {
				t.Errorf("type = %q, want %q", p.Type, wantType)
			}
			if tt.wantField != "" {
				if p.Errors == nil || len(*p.Errors) == 0 {
					t.Fatalf("no field errors in %s", body)
				}
				if got := (*p.Errors)[0].Pointer; got == nil || *got != tt.wantField {
					t.Errorf("pointer = %v, want %s: %s", got, tt.wantField, body)
				}
			}
			if strings.Contains(body, "example.com") {
				t.Errorf("body leaks the validator's internal schema URL: %s", body)
			}
			if tt.wantStatus == http.StatusMethodNotAllowed && resp.Header.Get("Allow") != tt.wantAllow {
				t.Errorf("Allow = %q, want %q", resp.Header.Get("Allow"), tt.wantAllow)
			}
			if tt.wantStatus == http.StatusUnauthorized && !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
			}
		})
	}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func TestDatabaseFailureDuringAuthIsNot401(t *testing.T) {
	f := newFixture(t)
	f.store.Pool().Close()
	resp, body := f.do(t, "GET", "/api/v1/sources", "", "", f.token)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500: %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Content-Type") != problemJSON {
		t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	if strings.Contains(body, "closed") {
		t.Errorf("body leaks the error: %s", body)
	}
}

func TestSpecNeedsNoToken(t *testing.T) {
	f := newFixture(t)
	resp, body := f.do(t, "GET", "/api/v1/openapi.json", "", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
}

// loadSpec returns the OpenAPI document this server is built from.
func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	spec, err := openapi3.NewLoader().LoadFromData(specfile.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// implemented lists the operations that do real work. Everything else in the
// spec must answer 501, and a phase that builds one moves it here.
var implemented = map[string]bool{
	"getSpec":        true,
	"listSources":    true,
	"createSource":   true,
	"getSource":      true,
	"updateSource":   true,
	"deleteSource":   true,
	"listEntries":    true,
	"listAPITokens":  true,
	"createAPIToken": true,
	"revokeAPIToken": true,
}

func TestUnimplementedOperationsAre501(t *testing.T) {
	f := newFixture(t)
	spec := loadSpec(t)
	checked := 0
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if implemented[op.OperationID] {
				continue
			}
			t.Run(op.OperationID, func(t *testing.T) {
				contentType, body := "", ""
				if op.RequestBody != nil {
					for mediaType, content := range op.RequestBody.Value.Content {
						contentType = mediaType
						if text, ok := content.Example.(string); ok {
							body = text
						} else if b, err := json.Marshal(content.Example); err == nil {
							body = string(b)
						}
					}
				}
				url := api.BasePath + strings.ReplaceAll(path, "{id}", "1")
				resp, data := f.do(t, method, url, contentType, body, f.token)
				if resp.StatusCode != http.StatusNotImplemented {
					t.Fatalf("status = %d, want 501: %s", resp.StatusCode, data)
				}
				var p client.Problem
				if err := json.Unmarshal([]byte(data), &p); err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(p.Type, "#not-implemented") {
					t.Errorf("type = %q", p.Type)
				}
			})
			checked++
		}
	}
	if checked == 0 {
		t.Error("no operations were checked")
	}
}

func TestEveryOperationIsDocumented(t *testing.T) {
	spec := loadSpec(t)
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		for method, op := range item.Operations() {
			if op.OperationID == "" || op.Summary == "" {
				t.Errorf("%s %s needs an operationId and a summary", method, path)
			}
			if seen[op.OperationID] {
				t.Errorf("duplicate operationId %s", op.OperationID)
			}
			seen[op.OperationID] = true
			if op.RequestBody != nil {
				for mediaType, content := range op.RequestBody.Value.Content {
					if content.Example == nil {
						t.Errorf("%s: request %s has no example", op.OperationID, mediaType)
					}
				}
			}
			for status, response := range op.Responses.Map() {
				for mediaType, content := range response.Value.Content {
					if content.Example == nil {
						t.Errorf("%s: response %s %s has no example", op.OperationID, status, mediaType)
					}
				}
			}
		}
	}
}

func TestExamplesMatchTheirSchemas(t *testing.T) {
	spec := loadSpec(t)
	check := func(name string, content *openapi3.MediaType) {
		t.Helper()
		if content.Schema == nil || content.Example == nil {
			return
		}
		// Through JSON, so the example has the types a real body would.
		data, err := json.Marshal(content.Example)
		if err != nil {
			t.Fatal(err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err := content.Schema.Value.VisitJSON(value, openapi3.DisableReadOnlyValidation(), openapi3.EnableFormatValidation()); err != nil {
			t.Errorf("%s: example does not match: %v", name, err)
		}
	}
	for _, item := range spec.Paths.Map() {
		for _, op := range item.Operations() {
			if op.RequestBody != nil {
				for mediaType, content := range op.RequestBody.Value.Content {
					check(op.OperationID+" request "+mediaType, content)
				}
			}
			for status, response := range op.Responses.Map() {
				for mediaType, content := range response.Value.Content {
					check(op.OperationID+" "+status+" "+mediaType, content)
				}
			}
		}
	}
}

func TestMetricsUseTheRoutePattern(t *testing.T) {
	f := newFixture(t)
	s := f.createSource(t, "metric")
	f.do(t, "GET", "/api/v1/sources/"+itoa(s.ID), "", "", f.token)
	f.do(t, "GET", "/api/v1/not-a-route-"+itoa(s.ID), "", "", f.token)

	got, err := testutil.GatherAndCount(f.registry, "andante_api_requests_total")
	if err != nil {
		t.Fatal(err)
	}
	if got == 0 {
		t.Fatal("andante_api_requests_total has no series")
	}
	families, err := f.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]bool{}
	for _, family := range families {
		if family.GetName() != "andante_api_requests_total" {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				if label.GetName() == "route" {
					labels[label.GetValue()] = true
				}
			}
		}
	}
	if !labels["GET /api/v1/sources/{id}"] {
		t.Errorf("no route pattern in %v", labels)
	}
	if !labels["unmatched"] {
		t.Errorf("unmatched route not labelled in %v", labels)
	}
	for route := range labels {
		if strings.Contains(route, "not-a-route") || strings.Contains(route, "/"+itoa(s.ID)) {
			t.Errorf("raw path in a label: %s", route)
		}
	}
}

func TestRequestID(t *testing.T) {
	f := newFixture(t)
	req, err := http.NewRequestWithContext(t.Context(), "GET", f.url+"/api/v1/openapi.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-ID", "abc-123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("X-Request-ID = %q, want the one sent", got)
	}

	req.Header.Set("X-Request-ID", "bad value\twith controls")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if got := resp2.Header.Get("X-Request-ID"); got == "" || strings.Contains(got, " ") {
		t.Errorf("X-Request-ID = %q, want a generated one", got)
	}
}
