// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	specfile "github.com/gerrowadat/andante/api"
	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/stacktest"
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
	actor    store.Actor
	stack    *stacktest.Stack
	url      string
	token    string
	store    *store.Store
	client   *client.ClientWithResponses
	registry *prometheus.Registry
}

func ptr[T any](v T) *T { return &v }

// newFixture serves the API on a real database, with one API token.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	stack := stacktest.New(t)
	st, reg := stack.Store, stack.Registry

	actor, token := storetest.User(t, st, "admin", true)
	c, err := client.NewClientWithResponses(stack.URL+api.BasePath, client.WithRequestEditorFn(bearer(token)))
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{actor: actor, url: stack.URL, token: token, store: st, client: c, registry: reg, stack: stack}
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
		Config:      crawlConfig(t, "https://comic.example/"+slug),
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
	if err != nil || crawl.StartURL != "https://comic.example/example" {
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

	_, err = f.store.Pool().Exec(ctx, "INSERT INTO reads (user_id, source_id, name) VALUES ($1, $2, 'r')", f.actor.UserID, created.ID)
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
	if err := feed.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: "ftp://feed.example/rss"}); err != nil {
		t.Fatal(err)
	}
	for name, config := range map[string]client.SourceConfig{
		"bad feed URL":  feed,
		"bad start URL": crawlConfig(t, "ftp://comic.example/"),
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

	const insert = `INSERT INTO entries (id, source_id, seq, kind, url, discovered_at, duration, enclosures)
		VALUES ((SELECT coalesce(max(id), 0) + 1 FROM entries), $1, $2, 'episode', $3, now(), '90 seconds', '[{"url":"https://a.example/1.mp3","type":"audio/mpeg","length":10}]')`
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

func TestProbe(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	site := sitetest.Serve(t, "wordpress-webcomic")

	limit := int32(3)
	resp, err := f.client.CreateProbeWithResponse(ctx, client.ProbeRequest{
		Config: crawlConfig(t, site.URL+"/comic/1/"),
		Limit:  &limit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.JSON200 == nil {
		t.Fatalf("probe: %d %s", resp.StatusCode(), resp.Body)
	}
	if got := len(resp.JSON200.Entries); got != 3 {
		t.Errorf("entries = %d, want 3", got)
	}
	if first := resp.JSON200.Entries[0]; first.Title.MustGet() != "Strip 1" || first.Seq != 0 || len(first.Images) != 1 {
		t.Errorf("first entry = %+v", first)
	}
	if resp.JSON200.DatedShare != 1 {
		t.Errorf("dated share = %v", resp.JSON200.DatedShare)
	}

	sources, err := f.client.ListSourcesWithResponse(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sources.JSON200 == nil || len(sources.JSON200.Items) != 0 {
		t.Errorf("a probe saved something: %s", sources.Body)
	}
}

func TestProbeFailures(t *testing.T) {
	f := newFixture(t)
	site := sitetest.Serve(t, "robots-disallow")
	for name, tt := range map[string]struct {
		config client.SourceConfig
		suffix string
	}{
		"site refuses": {crawlConfig(t, site.URL+"/comics/1.html"), "#probe-failed"},
		"bad config":   {crawlConfig(t, "ftp://comic.example/"), "#invalid-config"},
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := f.client.CreateProbeWithResponse(t.Context(), client.ProbeRequest{Config: tt.config})
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode() != http.StatusUnprocessableEntity || resp.ApplicationProblemJSONDefault == nil {
				t.Fatalf("got %d %s", resp.StatusCode(), resp.Body)
			}
			if got := resp.ApplicationProblemJSONDefault.Type; !strings.HasSuffix(got, tt.suffix) {
				t.Errorf("type = %q, want %s", got, tt.suffix)
			}
		})
	}
}

func TestRequestCrawl(t *testing.T) {
	f := newFixture(t)
	s := f.createSource(t, "due")
	resp, err := f.client.RequestCrawlWithResponse(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != http.StatusAccepted {
		t.Fatalf("crawl: %d %s", resp.StatusCode(), resp.Body)
	}
	got, err := f.client.GetSourceWithResponse(t.Context(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON200 == nil || got.JSON200.DueAt.IsNull() {
		t.Errorf("due_at not set: %s", got.Body)
	}
	missing, err := f.client.RequestCrawlWithResponse(t.Context(), 9999)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("crawl of a missing source: %d", missing.StatusCode())
	}
}

func (f *fixture) addEntries(t *testing.T, sourceID int64, n int, dated bool) {
	t.Helper()
	for i := range n {
		var date any
		if dated {
			date = time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*i)
		}
		_, err := f.store.Pool().Exec(t.Context(),
			"INSERT INTO entries (id, source_id, seq, kind, url, original_date, discovered_at, available_at) VALUES ((SELECT coalesce(max(id), 0) + 1 FROM entries), $1, $2, 'page', $3, $4, now() - interval '1 hour', now() - interval '1 hour')",
			sourceID, i, "https://comic.example/"+strconv.Itoa(i), date)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func rrulePacing(t *testing.T) client.Pacing {
	t.Helper()
	var p client.Pacing
	per := int32(1)
	if err := p.FromRRulePacing(client.RRulePacing{
		Mode: client.Rrule, Rrule: "FREQ=DAILY;BYHOUR=7;BYMINUTE=0;BYSECOND=0", Timezone: "Europe/Dublin", PerRelease: &per,
	}); err != nil {
		t.Fatal(err)
	}
	return p
}

func replayPacing(t *testing.T, speed float64) client.Pacing {
	t.Helper()
	var p client.Pacing
	if err := p.FromReplayPacing(client.ReplayPacing{Mode: client.Replay, Speed: speed}); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *fixture) createRead(t *testing.T, sourceID int64, p client.Pacing) client.Read {
	t.Helper()
	resp, err := f.client.CreateReadWithResponse(t.Context(), client.ReadCreate{SourceID: sourceID, Name: "Mornings", Pacing: p})
	if err != nil {
		t.Fatal(err)
	}
	if resp.JSON201 == nil {
		t.Fatalf("create read: %d %s", resp.StatusCode(), resp.Body)
	}
	return *resp.JSON201
}

func (f *fixture) adjust(t *testing.T, id int64, a client.Adjustment) *client.CreateAdjustmentResponse {
	t.Helper()
	resp, err := f.client.CreateAdjustmentWithResponse(t.Context(), id, a)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func skip(t *testing.T, count int32) client.Adjustment {
	t.Helper()
	var a client.Adjustment
	if err := a.FromSkipAdjustment(client.SkipAdjustment{Type: client.SkipAdjustmentTypeSkip, Count: &count}); err != nil {
		t.Fatal(err)
	}
	return a
}

func jump(t *testing.T, seq int32) client.Adjustment {
	t.Helper()
	var a client.Adjustment
	if err := a.FromJumpAdjustment(client.JumpAdjustment{Type: client.JumpAdjustmentTypeJump, Seq: seq}); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestReadLifecycle(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "comic")
	f.addEntries(t, s.ID, 20, true)

	read := f.createRead(t, s.ID, rrulePacing(t))
	if read.Position != 0 || read.State != client.ReadStateActive || read.IndexEnd.MustGet() != 19 || read.NextReleaseAt.IsNull() {
		t.Errorf("new read = %+v", read)
	}

	got, err := f.client.GetReadWithResponse(ctx, read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.JSON200 == nil || got.JSON200.Name != "Mornings" {
		t.Fatalf("get: %d %s", got.StatusCode(), got.Body)
	}

	skipped := f.adjust(t, read.ID, skip(t, 3))
	if skipped.JSON200 == nil || skipped.JSON200.Position != 3 {
		t.Fatalf("skip: %d %s", skipped.StatusCode(), skipped.Body)
	}

	var pause, resume client.Adjustment
	if err := pause.FromPauseAdjustment(client.PauseAdjustment{Type: client.PauseAdjustmentTypePause}); err != nil {
		t.Fatal(err)
	}
	if err := resume.FromResumeAdjustment(client.ResumeAdjustment{Type: client.ResumeAdjustmentTypeResume}); err != nil {
		t.Fatal(err)
	}
	paused := f.adjust(t, read.ID, pause)
	if paused.JSON200 == nil || paused.JSON200.State != client.ReadStatePaused || !paused.JSON200.NextReleaseAt.IsNull() {
		t.Fatalf("pause: %d %s", paused.StatusCode(), paused.Body)
	}
	if again := f.adjust(t, read.ID, pause); again.StatusCode() != http.StatusConflict {
		t.Errorf("pausing twice: %d %s", again.StatusCode(), again.Body)
	}
	if resumed := f.adjust(t, read.ID, resume); resumed.JSON200 == nil || resumed.JSON200.State != client.ReadStateActive {
		t.Fatalf("resume: %d %s", resumed.StatusCode(), resumed.Body)
	}

	jumped := f.adjust(t, read.ID, jump(t, 10))
	if jumped.JSON200 == nil || jumped.JSON200.Position != 10 {
		t.Fatalf("jump: %d %s", jumped.StatusCode(), jumped.Body)
	}
	backward := f.adjust(t, read.ID, jump(t, 5))
	if backward.StatusCode() != http.StatusConflict || backward.ApplicationProblemJSONDefault == nil {
		t.Errorf("jump backward: %d %s", backward.StatusCode(), backward.Body)
	}
	if same := f.adjust(t, read.ID, jump(t, 10)); same.StatusCode() != http.StatusConflict {
		t.Errorf("jump to the position: %d %s", same.StatusCode(), same.Body)
	}
	if past := f.adjust(t, read.ID, jump(t, 500)); past.StatusCode() != http.StatusConflict {
		t.Errorf("jump past the index: %d %s", past.StatusCode(), past.Body)
	}

	var repace client.Adjustment
	if err := repace.FromPacingAdjustment(client.PacingAdjustment{Type: client.PacingAdjustmentTypePacing, Pacing: replayPacing(t, 7)}); err != nil {
		t.Fatal(err)
	}
	if switched := f.adjust(t, read.ID, repace); switched.JSON200 == nil {
		t.Fatalf("change pacing: %d %s", switched.StatusCode(), switched.Body)
	}

	limit := int32(100)
	segments, err := f.client.ListSegmentsWithResponse(ctx, read.ID, &client.ListSegmentsParams{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if segments.JSON200 == nil {
		t.Fatalf("segments: %d %s", segments.StatusCode(), segments.Body)
	}
	var history []string
	for _, s := range segments.JSON200.Items {
		history = append(history, string(s.Reason))
	}
	if got, want := strings.Join(history, ","), "created,skip,pause,resume,jump,pacing"; got != want {
		t.Errorf("history = %s, want %s", got, want)
	}
	last := segments.JSON200.Items[len(segments.JSON200.Items)-1]
	if mode, err := last.Pacing.Discriminator(); err != nil || mode != "replay" {
		t.Errorf("last pacing = %q, %v", mode, err)
	}

	name := "Evenings"
	patched, err := f.client.UpdateReadWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, read.ID, client.ReadPatch{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	if patched.JSON200 == nil || patched.JSON200.Name != name {
		t.Fatalf("patch: %d %s", patched.StatusCode(), patched.Body)
	}
	buffer := int32(3)
	fixed, err := f.client.UpdateReadWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, read.ID, client.ReadPatch{Buffer: &buffer})
	if err != nil {
		t.Fatal(err)
	}
	if fixed.StatusCode() != http.StatusConflict {
		t.Errorf("patching buffer: %d %s", fixed.StatusCode(), fixed.Body)
	}

	list, err := f.client.ListReadsWithResponse(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.JSON200 == nil || len(list.JSON200.Items) != 1 {
		t.Fatalf("list: %d %s", list.StatusCode(), list.Body)
	}

	inUse, err := f.client.DeleteSourceWithResponse(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inUse.StatusCode() != http.StatusConflict {
		t.Errorf("deleting a source with a read: %d", inUse.StatusCode())
	}
	deleted, err := f.client.DeleteReadWithResponse(ctx, read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.StatusCode(), deleted.Body)
	}
	gone, err := f.client.GetReadWithResponse(ctx, read.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gone.StatusCode() != http.StatusNotFound {
		t.Errorf("get after delete: %d", gone.StatusCode())
	}
}

func TestReadRefusals(t *testing.T) {
	f := newFixture(t)
	s := f.createSource(t, "comic")
	f.addEntries(t, s.ID, 5, false)
	const js = "application/json"
	body := func(pacing string) string {
		return `{"source_id":` + itoa(s.ID) + `,"name":"r","pacing":` + pacing + `}`
	}
	tests := []struct {
		name, path, body string
		status           int
		suffix           string
	}{
		{"replay with too few dates", "/api/v1/reads", body(`{"mode":"replay","speed":7}`), 422, "#invalid-read"},
		{"unknown source", "/api/v1/reads", `{"source_id":9999,"name":"r","pacing":{"mode":"rrule","rrule":"FREQ=DAILY","timezone":"UTC"}}`, 422, "#invalid-read"},
		{"bad rule", "/api/v1/reads", body(`{"mode":"rrule","rrule":"nope","timezone":"UTC"}`), 422, "#invalid-read"},
		{"bad time zone", "/api/v1/reads", body(`{"mode":"rrule","rrule":"FREQ=DAILY","timezone":"Mars/Olympus"}`), 422, "#invalid-read"},
		{"too frequent", "/api/v1/reads", body(`{"mode":"rrule","rrule":"FREQ=MINUTELY","timezone":"UTC"}`), 422, "#invalid-read"},
		{"paused from the start", "/api/v1/reads", body(`{"mode":"paused"}`), 422, "#invalid-read"},
		{"a gap in months", "/api/v1/reads", body(`{"mode":"replay","speed":1,"default_gap":"P1M"}`), 422, "#invalid-read"},
		{"zero speed", "/api/v1/reads", body(`{"mode":"replay","speed":0,"default_gap":"P1D"}`), 400, "#invalid-request"},
		{"unknown read", "/api/v1/reads/9999/adjustments", `{"type":"pause"}`, 404, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, data := f.do(t, "POST", tt.path, js, tt.body, f.token)
			if resp.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode, tt.status, data)
			}
			if resp.Header.Get("Content-Type") != problemJSON {
				t.Errorf("Content-Type = %q", resp.Header.Get("Content-Type"))
			}
			if tt.suffix != "" && !strings.Contains(data, tt.suffix) {
				t.Errorf("type is not %s: %s", tt.suffix, data)
			}
		})
	}

	missing, err := f.client.ListSegmentsWithResponse(t.Context(), 9999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("segments of a missing read: %d", missing.StatusCode())
	}
}

func TestReplayWithGapsThroughTheAPI(t *testing.T) {
	f := newFixture(t)
	s := f.createSource(t, "podcast")
	f.addEntries(t, s.ID, 5, true)
	resp, data := f.do(t, "POST", "/api/v1/reads", "application/json",
		`{"source_id":`+itoa(s.ID)+`,"name":"r","pacing":{"mode":"replay","speed":7,"min_gap":"PT12H","max_gap":"P3D"}}`, f.token)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d: %s", resp.StatusCode, data)
	}
	var read client.Read
	if err := json.Unmarshal([]byte(data), &read); err != nil {
		t.Fatal(err)
	}
	segments, err := f.client.ListSegmentsWithResponse(t.Context(), read.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if segments.JSON200 == nil {
		t.Fatalf("segments: %d %s", segments.StatusCode(), segments.Body)
	}
	replay, err := segments.JSON200.Items[0].Pacing.AsReplayPacing()
	if err != nil {
		t.Fatal(err)
	}
	if replay.MinGap == nil || replay.MaxGap == nil || replay.DefaultGap != nil {
		t.Errorf("gaps = %+v", replay)
	}
}

func TestFeedTokens(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "comic")
	read := f.createRead(t, s.ID, rrulePacing(t))

	created, err := f.client.CreateFeedTokenWithResponse(ctx, read.ID, client.FeedTokenCreate{Role: client.Viewer, Label: "Alex"})
	if err != nil {
		t.Fatal(err)
	}
	if created.JSON201 == nil {
		t.Fatalf("create: %d %s", created.StatusCode(), created.Body)
	}
	issued := created.JSON201
	if issued.Suggested != client.IssuedFeedTokenSuggestedAtom {
		t.Errorf("suggested = %q", issued.Suggested)
	}
	want := "https://andante.example/f/" + issued.Token + ".rss"
	if issued.Urls.Rss != want {
		t.Errorf("rss = %q, want %q", issued.Urls.Rss, want)
	}
	if !strings.HasSuffix(issued.Urls.Atom, ".atom") || !strings.HasSuffix(issued.Urls.JSON, ".json") {
		t.Errorf("urls = %+v", issued.Urls)
	}

	list, err := f.client.ListFeedTokensWithResponse(ctx, read.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.JSON200 == nil || len(list.JSON200.Items) != 1 {
		t.Fatalf("list: %d %s", list.StatusCode(), list.Body)
	}
	if strings.Contains(string(list.Body), `"token"`) {
		t.Errorf("list has a token field: %s", list.Body)
	}
	if urls := list.JSON200.Items[0].Urls; urls == nil || *urls != issued.Urls {
		t.Errorf("list URLs = %+v, want %+v", urls, issued.Urls)
	}

	revoked, err := f.client.RevokeFeedTokenWithResponse(ctx, issued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked.StatusCode() != http.StatusNoContent {
		t.Fatalf("revoke: %d %s", revoked.StatusCode(), revoked.Body)
	}
	list, err = f.client.ListFeedTokensWithResponse(ctx, read.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if list.JSON200.Items[0].RevokedAt.IsNull() {
		t.Error("revoked_at is empty")
	}

	again, err := f.client.RevokeFeedTokenWithResponse(ctx, issued.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.StatusCode() != http.StatusNoContent {
		t.Errorf("revoking twice: %d", again.StatusCode())
	}
	missing, err := f.client.RevokeFeedTokenWithResponse(ctx, 9999)
	if err != nil {
		t.Fatal(err)
	}
	if missing.StatusCode() != http.StatusNotFound {
		t.Errorf("revoking a missing token: %d", missing.StatusCode())
	}
	noRead, err := f.client.CreateFeedTokenWithResponse(ctx, 9999, client.FeedTokenCreate{Role: client.Viewer, Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if noRead.StatusCode() != http.StatusNotFound {
		t.Errorf("token for a missing read: %d", noRead.StatusCode())
	}
	noList, err := f.client.ListFeedTokensWithResponse(ctx, 9999, nil)
	if err != nil {
		t.Fatal(err)
	}
	if noList.StatusCode() != http.StatusNotFound {
		t.Errorf("tokens of a missing read: %d", noList.StatusCode())
	}
}

func TestSuggestedFormatFollowsTheKind(t *testing.T) {
	f := newFixture(t)
	created, err := f.client.CreateSourceWithResponse(t.Context(), client.SourceCreate{
		Slug: "podcast", Title: "P", HomeURL: "https://p.example/", DefaultKind: client.Episode,
		Config: crawlConfig(t, "https://p.example/1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := f.createRead(t, created.JSON201.ID, rrulePacing(t))
	token, err := f.client.CreateFeedTokenWithResponse(t.Context(), read.ID, client.FeedTokenCreate{Role: client.Viewer, Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if token.JSON201 == nil || token.JSON201.Suggested != client.IssuedFeedTokenSuggestedRss {
		t.Errorf("suggested: %s", token.Body)
	}
}

func TestExportOPML(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "comic")
	first, second := f.createRead(t, s.ID, rrulePacing(t)), f.createRead(t, s.ID, rrulePacing(t))

	resp, body := f.do(t, "POST", "/api/v1/exports/opml", "application/json", `{"issue":"viewer","label":"For Alex"}`, f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/x-opml" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := strings.Count(body, "<outline"); got != 2 {
		t.Errorf("outlines = %d, want 2: %s", got, body)
	}
	if !strings.Contains(body, `version="2.0"`) || !strings.Contains(body, "https://andante.example/f/") {
		t.Errorf("body = %s", body)
	}
	for _, read := range []client.Read{first, second} {
		tokens, err := f.client.ListFeedTokensWithResponse(ctx, read.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(tokens.JSON200.Items) != 1 || tokens.JSON200.Items[0].Label != "For Alex" || tokens.JSON200.Items[0].Role != client.Viewer {
			t.Errorf("tokens of read %d: %s", read.ID, tokens.Body)
		}
	}

	if bad, data := f.do(t, "POST", "/api/v1/exports/opml", "application/json", `{"label":"x"}`, f.token); bad.StatusCode != http.StatusBadRequest {
		t.Errorf("no issue: %d %s", bad.StatusCode, data)
	}
}

func TestImportOPML(t *testing.T) {
	f := newFixture(t)
	f.do(t, "POST", "/api/v1/sources", "application/json",
		`{"slug":"known","title":"Known","home_url":"https://k.example/","default_kind":"article","config":{"strategy":"feed","feed_url":"https://k.example/rss"}}`, f.token)
	const miniflux = `<?xml version="1.0" encoding="UTF-8"?>
<opml version="2.0">
  <head><title>Miniflux</title></head>
  <body>
    <outline text="All">
      <outline title="A Blog" text="A Blog" xmlUrl="https://blog.example/feed" htmlUrl="https://blog.example/"></outline>
      <outline title="Same Title" text="Same Title" xmlUrl="https://one.example/feed"></outline>
      <outline title="Same Title" text="Same Title" xmlUrl="https://two.example/feed"></outline>
      <outline title="Repeated" xmlUrl="https://blog.example/feed"></outline>
      <outline title="Known" xmlUrl="https://k.example/rss"></outline>
      <outline title="Broken" xmlUrl="ftp://nope.example/feed"></outline>
      <outline title="No Feed"></outline>
    </outline>
  </body>
</opml>`
	resp, body := f.do(t, "POST", "/api/v1/imports/opml", "text/x-opml", miniflux, f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var result client.OPMLImportResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range result.Results {
		got = append(got, string(r.Status))
	}
	want := "created,created,created,duplicate,duplicate,invalid,invalid"
	if strings.Join(got, ",") != want {
		t.Errorf("results = %v, want %s: %s", got, want, body)
	}

	sources, err := f.client.ListSourcesWithResponse(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	slugs := map[string]client.SourceState{}
	for _, s := range sources.JSON200.Items {
		slugs[s.Slug] = s.State
	}
	for _, slug := range []string{"a-blog", "same-title", "same-title-2"} {
		if slugs[slug] != client.SourceStatePaused {
			t.Errorf("%s: state %q, want paused: %v", slug, slugs[slug], slugs)
		}
	}

	if bad, data := f.do(t, "POST", "/api/v1/imports/opml", "text/x-opml", "not xml", f.token); bad.StatusCode != http.StatusBadRequest {
		t.Errorf("not OPML: %d %s", bad.StatusCode, data)
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
		err := f.store.Pool().QueryRow(ctx, "SELECT last_used_at::text FROM api_tokens WHERE label = 'first token'").Scan(&v)
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

func TestVersionNeedsNoToken(t *testing.T) {
	f := newFixture(t)
	resp, body := f.do(t, "GET", "/api/v1/version", "", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var v client.Version
	if err := json.Unmarshal([]byte(body), &v); err != nil || v.Version == "" {
		t.Errorf("version = %+v, %v", v, err)
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

func TestReadsAndFeedsDoNotNeedTheCatalogue(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	created, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "s", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, "https://comic.example/1"),
	})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create source: %v %s", err, created.Body)
	}
	id := created.JSON201.ID
	f.stack.CatalogueServer.Close()

	listed, err := f.client.GetSourceWithResponse(ctx, id)
	if err != nil || listed.JSON200 == nil {
		t.Errorf("get source with the catalogue down: %v %d", err, listed.StatusCode())
	}
	f.addEntries(t, id, 2, true)
	read, err := f.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: id, Name: "r", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create a read with the catalogue down: %v %s", err, read.Body)
	}

	down, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "t", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, "https://comic.example/2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if down.StatusCode() != http.StatusServiceUnavailable || down.ApplicationProblemJSONDefault == nil ||
		!strings.HasSuffix(down.ApplicationProblemJSONDefault.Type, "#catalogue-unavailable") {
		t.Errorf("create with the catalogue down: %d %s", down.StatusCode(), down.Body)
	}
}

type other struct {
	actor  store.Actor
	client *client.ClientWithResponses
	token  string
}

func (f *fixture) user(t *testing.T, name string, admin bool) other {
	t.Helper()
	actor, token := storetest.User(t, f.store, name, admin)
	c, err := client.NewClientWithResponses(f.url+api.BasePath, client.WithRequestEditorFn(bearer(token)))
	if err != nil {
		t.Fatal(err)
	}
	return other{actor: actor, client: c, token: token}
}

// A list of everything that takes an ID belonging to someone. The test walks the spec,
// so a new operation fails here until it says what it does.
var scoped = map[string]struct {
	body, contentType string
}{
	"getRead":          {},
	"deleteRead":       {},
	"updateRead":       {`{"name":"mine now"}`, "application/merge-patch+json"},
	"createAdjustment": {`{"type":"skip"}`, "application/json"},
	"listSegments":     {},
	"listFeedTokens":   {},
	"createFeedToken":  {`{"role":"viewer","label":"me"}`, "application/json"},
	"revokeFeedToken":  {},
	"revokeAPIToken":   {},
	"revokeSession":    {},
	"deleteInvite":     {},
}

func TestUsersCantReachEachOthersThings(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "s").ID
	f.addEntries(t, s, 3, true)
	alice, mallory := f.user(t, "alice", false), f.user(t, "mallory", false)
	read, err := alice.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: s, Name: "alice's", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	readID := read.JSON201.ID
	token, err := alice.client.CreateFeedTokenWithResponse(ctx, readID, client.FeedTokenCreate{Role: client.Owner, Label: "me"})
	if err != nil || token.JSON201 == nil {
		t.Fatalf("create token: %v %s", err, token.Body)
	}
	second, err := alice.client.CreateAPITokenWithResponse(ctx, client.APITokenCreate{Label: "laptop"})
	if err != nil || second.JSON201 == nil {
		t.Fatalf("create API token: %v %s", err, second.Body)
	}

	session := f.session(t, alice)
	users := store.InvitesByUsers
	if err := f.store.SetInviteSettings(ctx, &users, nil); err != nil {
		t.Fatal(err)
	}
	invite, err := alice.client.CreateInviteWithResponse(ctx, client.InviteCreate{})
	if err != nil || invite.JSON201 == nil {
		t.Fatalf("create invite: %v %s", err, invite.Body)
	}

	spec, err := openapi3.NewLoader().LoadFromData(specfile.OpenAPI)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for path, item := range spec.Paths.Map() {
		var id int64
		switch {
		case strings.HasPrefix(path, "/reads/{id}"):
			id = readID
		case strings.HasPrefix(path, "/tokens/{id}"):
			id = token.JSON201.ID
		case strings.HasPrefix(path, "/api-tokens/{id}"):
			id = second.JSON201.ID
		case strings.HasPrefix(path, "/sessions/{id}"):
			id = session
		case strings.HasPrefix(path, "/invites/{id}"):
			id = invite.JSON201.ID
		default:
			continue
		}
		for method, op := range item.Operations() {
			want, ok := scoped[op.OperationID]
			if !ok {
				t.Errorf("%s isn't in the table of what's scoped to a user", op.OperationID)
				continue
			}
			seen[op.OperationID] = true
			t.Run(op.OperationID, func(t *testing.T) {
				url := strings.ReplaceAll(path, "{id}", strconv.FormatInt(id, 10))
				resp, body := f.do(t, method, api.BasePath+url, want.contentType, want.body, mallory.token)
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("status = %d, want 404: %s", resp.StatusCode, body)
				}
			})
		}
	}
	for id := range scoped {
		if !seen[id] {
			t.Errorf("%s is in the table, but not under an owned path", id)
		}
	}

	// Nothing changed underneath alice.
	got, err := alice.client.GetReadWithResponse(ctx, readID)
	if err != nil || got.JSON200 == nil || got.JSON200.Name != "alice's" || got.JSON200.Position != 0 {
		t.Errorf("alice's read after all that: %v %s", err, got.Body)
	}
	listed, err := alice.client.ListFeedTokensWithResponse(ctx, readID, nil)
	if err != nil || listed.JSON200 == nil || listed.JSON200.Items[0].RevokedAt.IsSpecified() && !listed.JSON200.Items[0].RevokedAt.IsNull() {
		t.Errorf("alice's feed token after all that: %v %s", err, listed.Body)
	}

	all := true
	for name, params := range map[string]*client.ListReadsParams{"own": nil, "all": {All: &all}} {
		reads, err := mallory.client.ListReadsWithResponse(ctx, params)
		if err != nil || reads.JSON200 == nil || len(reads.JSON200.Items) != 0 {
			t.Errorf("%s: mallory's reads = %s, %v", name, reads.Body, err)
		}
	}
	tokens, err := mallory.client.ListAPITokensWithResponse(ctx, nil)
	if err != nil || tokens.JSON200 == nil || len(tokens.JSON200.Items) != 1 {
		t.Errorf("mallory's API tokens = %s, %v", tokens.Body, err)
	}
	opml, err := mallory.client.ExportOPMLWithResponse(ctx, client.OPMLExportRequest{Issue: client.Viewer, Label: "x"})
	if err != nil || strings.Contains(string(opml.Body), "alice") {
		t.Errorf("mallory's export = %s, %v", opml.Body, err)
	}
}

func TestAdminsCanSeeEveryRead(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "s").ID
	f.addEntries(t, s, 3, true)
	alice := f.user(t, "alice", false)
	read, err := alice.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: s, Name: "r", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	if read.JSON201.UserID != alice.actor.UserID {
		t.Errorf("user_id = %d, want %d", read.JSON201.UserID, alice.actor.UserID)
	}

	own, err := f.client.ListReadsWithResponse(ctx, nil)
	if err != nil || len(own.JSON200.Items) != 0 {
		t.Errorf("an admin's own reads = %s, %v", own.Body, err)
	}
	everyone := true
	all, err := f.client.ListReadsWithResponse(ctx, &client.ListReadsParams{All: &everyone})
	if err != nil || len(all.JSON200.Items) != 1 {
		t.Errorf("all reads = %s, %v", all.Body, err)
	}
	got, err := f.client.GetReadWithResponse(ctx, read.JSON201.ID)
	if err != nil || got.JSON200 == nil {
		t.Errorf("an admin reading alice's read: %v %s", err, got.Body)
	}
}

func TestSourcesAreSharedButOnlyAdminsChangeThem(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)

	created, err := bob.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "bobs", Title: "Bob's Comic", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, "https://comic.example/1"),
	})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("a user adding a source: %v %s", err, created.Body)
	}
	id := created.JSON201.ID

	seen, err := f.client.GetSourceWithResponse(ctx, id)
	if err != nil || seen.JSON200 == nil {
		t.Errorf("another user seeing it: %v", err)
	}
	title := "Mine"
	patched, err := bob.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, id, client.SourcePatch{Title: &title})
	if err != nil || patched.StatusCode() != http.StatusForbidden {
		t.Errorf("a user changing a source: %v %d", err, patched.StatusCode())
	}
	if patched.ApplicationProblemJSONDefault == nil || !strings.HasSuffix(patched.ApplicationProblemJSONDefault.Type, "#forbidden") {
		t.Errorf("problem = %s", patched.Body)
	}
	deleted, err := bob.client.DeleteSourceWithResponse(ctx, id)
	if err != nil || deleted.StatusCode() != http.StatusForbidden {
		t.Errorf("a user deleting a source: %v %d", err, deleted.StatusCode())
	}
	asAdmin, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, id, client.SourcePatch{Title: &title})
	if err != nil || asAdmin.StatusCode() != http.StatusOK {
		t.Errorf("an admin changing a source: %v %d", err, asAdmin.StatusCode())
	}
}

func TestSourceSearch(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.createSource(t, "harbour-lights")
	created, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "pod", Title: "A Podcast", HomeURL: "https://pod.example/", DefaultKind: client.Episode, Config: crawlConfig(t, "https://pod.example/1"),
	})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create: %v %s", err, created.Body)
	}
	slugs := func(params *client.ListSourcesParams) string {
		t.Helper()
		resp, err := f.client.ListSourcesWithResponse(ctx, params)
		if err != nil || resp.JSON200 == nil {
			t.Fatalf("list: %v", err)
		}
		var out []string
		for _, s := range resp.JSON200.Items {
			out = append(out, s.Slug)
		}
		return strings.Join(out, ",")
	}
	q := "HARBOUR"
	if got := slugs(&client.ListSourcesParams{Q: &q}); got != "harbour-lights" {
		t.Errorf("search = %q", got)
	}
	podcast := "podcast"
	if got := slugs(&client.ListSourcesParams{Q: &podcast}); got != "pod" {
		t.Errorf("search by title = %q", got)
	}
	episode := client.Episode
	if got := slugs(&client.ListSourcesParams{Kind: &episode}); got != "pod" {
		t.Errorf("kind = %q", got)
	}
	if got := slugs(nil); got != "harbour-lights,pod" {
		t.Errorf("no filter = %q", got)
	}
}

func TestMe(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob", false)
	me, err := bob.client.GetMeWithResponse(t.Context())
	if err != nil || me.JSON200 == nil || me.JSON200.Name != "bob" || me.JSON200.Role != client.UserRoleUser {
		t.Errorf("me = %s, %v", me.Body, err)
	}
	admin, err := f.client.GetMeWithResponse(t.Context())
	if err != nil || admin.JSON200 == nil || admin.JSON200.Role != client.UserRoleAdmin {
		t.Errorf("admin = %s, %v", admin.Body, err)
	}
}

func TestADisabledUserCantUseTheirToken(t *testing.T) {
	f := newFixture(t)
	bob := f.user(t, "bob", false)
	if _, err := f.store.Pool().Exec(t.Context(), "UPDATE users SET disabled_at = now() WHERE id = $1", bob.actor.UserID); err != nil {
		t.Fatal(err)
	}
	me, err := bob.client.GetMeWithResponse(t.Context())
	if err != nil || me.StatusCode() != http.StatusUnauthorized {
		t.Errorf("disabled user: %v %d", err, me.StatusCode())
	}
}

func TestEachUserKeepsTheirLastToken(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	tokens, err := bob.client.ListAPITokensWithResponse(ctx, nil)
	if err != nil || tokens.JSON200 == nil {
		t.Fatalf("list: %v", err)
	}
	last, err := bob.client.RevokeAPITokenWithResponse(ctx, tokens.JSON200.Items[0].ID)
	if err != nil || last.StatusCode() != http.StatusConflict {
		t.Errorf("revoking bob's last token: %v %d, though the admin has one too", err, last.StatusCode())
	}
}

func TestASerialCantBeAddedTwice(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	first, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "first", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, "https://comic.example/1"),
	})
	if err != nil || first.JSON201 == nil {
		t.Fatalf("create: %v %s", err, first.Body)
	}

	for _, address := range []string{"https://comic.example/1", "https://COMIC.example:443/1/", "https://comic.example/1#top"} {
		again, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
			Slug: "again", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, address),
		})
		if err != nil {
			t.Fatal(err)
		}
		if again.StatusCode() != http.StatusConflict {
			t.Errorf("%s: %d %s", address, again.StatusCode(), again.Body)
			continue
		}
		problem := again.ApplicationProblemJSONDefault
		if problem == nil || problem.ExistingSourceID == nil || *problem.ExistingSourceID != first.JSON201.ID {
			t.Errorf("%s: problem = %s", address, again.Body)
		}
	}

	slug, err := f.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "first", Title: "t", HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: crawlConfig(t, "https://comic.example/2"),
	})
	if err != nil || slug.StatusCode() != http.StatusConflict || slug.ApplicationProblemJSONDefault.ExistingSourceID != nil {
		t.Errorf("a slug in use: %v %s", err, slug.Body)
	}

	other := f.createSource(t, "other")
	config := crawlConfig(t, "https://comic.example/1/")
	moved, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, other.ID, client.SourcePatch{Config: &config})
	if err != nil || moved.StatusCode() != http.StatusConflict || moved.ApplicationProblemJSONDefault == nil ||
		moved.ApplicationProblemJSONDefault.ExistingSourceID == nil || *moved.ApplicationProblemJSONDefault.ExistingSourceID != first.JSON201.ID {
		t.Errorf("changing a config to one in use: %v %s", err, moved.Body)
	}
	own := crawlConfig(t, "https://comic.example/1/")
	unchanged, err := f.client.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, first.JSON201.ID, client.SourcePatch{Config: &own})
	if err != nil || unchanged.StatusCode() != http.StatusOK {
		t.Errorf("a source keeping its own key: %v %s", err, unchanged.Body)
	}
}

func TestAnImportCatchesADuplicateSpeltDifferently(t *testing.T) {
	f := newFixture(t)
	f.do(t, "POST", "/api/v1/sources", "application/json",
		`{"slug":"known","title":"Known","home_url":"https://k.example/","default_kind":"article","config":{"strategy":"feed","feed_url":"https://k.example/rss"}}`, f.token)
	opml := `<opml version="2.0"><body><outline title="Known" xmlUrl="https://K.example/rss/"/></body></opml>`
	resp, body := f.do(t, "POST", "/api/v1/imports/opml", "text/x-opml", opml, f.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.StatusCode, body)
	}
	var result client.OPMLImportResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	if got := result.Results[0]; got.Status != client.OPMLOutlineResultStatusDuplicate || got.SourceID == nil {
		t.Errorf("result = %s", body)
	}
}

func TestAReadStartsASourceNobodyHasLookedAt(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	opml := `<opml version="2.0"><body><outline title="Blog" xmlUrl="https://blog.example/rss"/></body></opml>`
	resp, body := f.do(t, "POST", "/api/v1/imports/opml", "text/x-opml", opml, bob.token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}
	var result client.OPMLImportResult
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	id := *result.Results[0].SourceID
	state := func() client.SourceState {
		t.Helper()
		got, err := bob.client.GetSourceWithResponse(ctx, id)
		if err != nil || got.JSON200 == nil {
			t.Fatalf("get: %v", err)
		}
		return got.JSON200.State
	}
	if got := state(); got != client.SourceStatePaused {
		t.Fatalf("after the import: %q", got)
	}
	read, err := bob.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: id, Name: "r", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	if got := state(); got != client.SourceStateIndexing {
		t.Errorf("after the read: %q, want indexing", got)
	}
}

func TestAReadLeavesASourcePausedOnPurpose(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	source := f.createSource(t, "s")
	_, err := f.stack.Catalogue.Pool().Exec(ctx, "UPDATE sources SET state = 'paused', last_crawled_at = now() WHERE id = $1", source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.stack.Sync.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	bob := f.user(t, "bob", false)
	read, err := bob.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: source.ID, Name: "r", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	got, err := bob.client.GetSourceWithResponse(ctx, source.ID)
	if err != nil || got.JSON200 == nil || got.JSON200.State != client.SourceStatePaused {
		t.Errorf("state = %s, %v", got.Body, err)
	}
}

func TestDiscoveries(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	site := sitetest.Serve(t, "discover-full")

	limit := int32(2)
	found, err := bob.client.CreateDiscoveryWithResponse(ctx, client.DiscoveryRequest{URL: site.URL + "/", Limit: &limit})
	if err != nil || found.JSON200 == nil {
		t.Fatalf("discover: %v %s", err, found.Body)
	}
	if len(found.JSON200.Candidates) != 1 {
		t.Fatalf("candidates = %s", found.Body)
	}
	c := found.JSON200.Candidates[0]
	if c.Title != "A Full Blog" || len(c.Sample) != 2 || c.ExistingSourceID.IsSpecified() && !c.ExistingSourceID.IsNull() {
		t.Errorf("candidate = %s", found.Body)
	}

	created, err := bob.client.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "blog", Title: c.Title, HomeURL: site.URL + "/", DefaultKind: c.DefaultKind, Config: c.Config,
	})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create from a candidate: %v %s", err, created.Body)
	}
	before := len(site.Requests())
	again, err := bob.client.CreateDiscoveryWithResponse(ctx, client.DiscoveryRequest{URL: site.URL})
	if err != nil || again.JSON200 == nil || len(again.JSON200.Candidates) != 1 {
		t.Fatalf("second discovery: %v %s", err, again.Body)
	}
	if id, err := again.JSON200.Candidates[0].ExistingSourceID.Get(); err != nil || id != created.JSON201.ID {
		t.Errorf("second discovery: %s", again.Body)
	}
	if got := len(site.Requests()); got != before {
		t.Errorf("the second discovery made %d requests", got-before)
	}

	bad, err := bob.client.CreateDiscoveryWithResponse(ctx, client.DiscoveryRequest{URL: "ftp://a.example/"})
	if err != nil || bad.StatusCode() != http.StatusUnprocessableEntity {
		t.Errorf("a bad scheme: %v %d", err, bad.StatusCode())
	}
	blocked := sitetest.Serve(t, "robots-disallow")
	refused, err := bob.client.CreateDiscoveryWithResponse(ctx, client.DiscoveryRequest{URL: blocked.URL + "/comics/1.html"})
	if err != nil || refused.StatusCode() != http.StatusUnprocessableEntity || refused.ApplicationProblemJSONDefault == nil ||
		!strings.HasSuffix(refused.ApplicationProblemJSONDefault.Type, "#probe-failed") {
		t.Errorf("a site that says no: %v %s", err, refused.Body)
	}

	f.stack.CatalogueServer.Close()
	down, err := bob.client.CreateDiscoveryWithResponse(ctx, client.DiscoveryRequest{URL: site.URL})
	if err != nil || down.StatusCode() != http.StatusServiceUnavailable {
		t.Errorf("the catalogue down: %v %d", err, down.StatusCode())
	}
}

// session logs a user in, and returns the session's ID.
func (f *fixture) session(t *testing.T, u other) int64 {
	t.Helper()
	if _, err := f.store.CreateSession(t.Context(), u.actor.UserID, store.Client{IP: "203.0.113.1", UserAgent: "test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	sessions, err := u.client.ListSessionsWithResponse(t.Context(), nil)
	if err != nil || sessions.JSON200 == nil || len(sessions.JSON200.Items) == 0 {
		t.Fatalf("list sessions: %v", err)
	}
	return sessions.JSON200.Items[len(sessions.JSON200.Items)-1].ID
}

func TestSessions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	id := f.session(t, bob)

	got, err := bob.client.ListSessionsWithResponse(ctx, nil)
	if err != nil || got.JSON200 == nil || len(got.JSON200.Items) != 1 || got.JSON200.Items[0].IP != "203.0.113.1" {
		t.Fatalf("sessions = %s, %v", got.Body, err)
	}
	// Someone else's is a 404, even for an admin.
	admin, err := f.client.RevokeSessionWithResponse(ctx, id)
	if err != nil || admin.StatusCode() != http.StatusNotFound {
		t.Errorf("an admin ending bob's session: %v %d", err, admin.StatusCode())
	}
	ended, err := bob.client.RevokeSessionWithResponse(ctx, id)
	if err != nil || ended.StatusCode() != http.StatusNoContent {
		t.Fatalf("ending it: %v %d", err, ended.StatusCode())
	}
	left, err := bob.client.ListSessionsWithResponse(ctx, nil)
	if err != nil || left.JSON200 == nil || len(left.JSON200.Items) != 0 {
		t.Errorf("sessions left = %s, %v", left.Body, err)
	}
}

func TestInvitesAreForAdmins(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)

	refused, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("alex")})
	if err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user inviting: %v %d", err, refused.StatusCode())
	}
	created, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("alex")})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("an admin inviting: %v %s", err, created.Body)
	}
	if created.JSON201.Role != client.InviteRoleUser || created.JSON201.MaxUses != 1 || !strings.HasPrefix(created.JSON201.URL, "https://andante.example/invite/") {
		t.Errorf("invite = %s", created.Body)
	}
	taken, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("bob")})
	if err != nil || taken.StatusCode() != http.StatusConflict {
		t.Errorf("a taken name: %v %d", err, taken.StatusCode())
	}
	reset := true
	again, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("bob"), Reset: &reset})
	if err != nil || again.JSON201 == nil {
		t.Errorf("a reset: %v %s", err, again.Body)
	}
	nobody, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("nobody"), Reset: &reset})
	if err != nil || nobody.StatusCode() != http.StatusConflict {
		t.Errorf("a reset for nobody: %v %d", err, nobody.StatusCode())
	}
	bad, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("Not A Name")})
	if err != nil || bad.StatusCode() != http.StatusBadRequest {
		t.Errorf("a bad name: %v %d", err, bad.StatusCode())
	}

	three, far := int32(3), time.Now().Add(31*24*time.Hour)
	for what, body := range map[string]client.InviteCreate{
		"a name for three":                 {Name: ptr("sam"), MaxUses: &three},
		"a reset for three":                {Name: ptr("bob"), Reset: &reset, MaxUses: &three},
		"a reset for nobody in particular": {Reset: &reset},
		"31 days":                          {ExpiresAt: &far},
	} {
		got, err := f.client.CreateInviteWithResponse(ctx, body)
		if err != nil || got.StatusCode() != http.StatusBadRequest {
			t.Errorf("%s: %v %d", what, err, got.StatusCode())
		}
	}
	open, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &three})
	if err != nil || open.JSON201 == nil || open.JSON201.Name != "" || open.JSON201.MaxUses != 3 {
		t.Fatalf("a link for three: %v %s", err, open.Body)
	}
}

func TestUsersInviteWhenTheSiteSaysSo(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob, carol := f.user(t, "bob", false), f.user(t, "carol", false)
	users, limit := client.WhoCanInvite("users"), int32(3)
	two, one := int32(2), int32(1)

	settings, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{WhoCanInvite: &users, InviteLimit: &limit})
	if err != nil || settings.JSON200 == nil || settings.JSON200.WhoCanInvite != users || settings.JSON200.InviteLimit != 3 {
		t.Fatalf("let users invite: %v %s", err, settings.Body)
	}

	admin, reset := client.InviteCreateRoleAdmin, true
	for what, body := range map[string]client.InviteCreate{
		"an admin": {Role: &admin},
		"a reset":  {Name: ptr("carol"), Reset: &reset},
	} {
		got, err := bob.client.CreateInviteWithResponse(ctx, body)
		if err != nil || got.StatusCode() != http.StatusForbidden {
			t.Errorf("bob inviting %s: %v %d", what, err, got.StatusCode())
		}
	}

	first, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &two})
	if err != nil || first.JSON201 == nil || first.JSON201.Role != client.InviteRoleUser {
		t.Fatalf("bob's first: %v %s", err, first.Body)
	}
	over, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &two})
	if err != nil || over.StatusCode() != http.StatusConflict {
		t.Errorf("past the limit: %v %d %s", err, over.StatusCode(), over.Body)
	}
	if got, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &one}); err != nil || got.JSON201 == nil {
		t.Errorf("up to the limit: %v %s", err, got.Body)
	}
	if got, err := carol.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &two}); err != nil || got.JSON201 == nil {
		t.Errorf("carol has her own places: %v %s", err, got.Body)
	}
	if got, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: ptr(int32(100))}); err != nil || got.JSON201 == nil {
		t.Errorf("an admin has no limit: %v %s", err, got.Body)
	}

	mine, err := bob.client.ListInvitesWithResponse(ctx, nil)
	if err != nil || mine.JSON200 == nil || len(mine.JSON200.Items) != 2 {
		t.Fatalf("bob's invites: %v %s", err, mine.Body)
	}
	if i := mine.JSON200.Items[0]; i.ID != first.JSON201.ID || i.MaxUses != 2 || i.Uses != 0 || i.CreatedBy == nil || *i.CreatedBy != "bob" {
		t.Errorf("bob's first, listed: %+v", i)
	}
	everyone, err := f.client.ListInvitesWithResponse(ctx, nil)
	if err != nil || everyone.JSON200 == nil || len(everyone.JSON200.Items) != 4 {
		t.Errorf("everyone's invites: %v %s", err, everyone.Body)
	}

	if got, err := carol.client.DeleteInviteWithResponse(ctx, first.JSON201.ID); err != nil || got.StatusCode() != http.StatusNotFound {
		t.Errorf("carol withdrawing bob's: %v %d", err, got.StatusCode())
	}
	if got, err := bob.client.DeleteInviteWithResponse(ctx, first.JSON201.ID); err != nil || got.StatusCode() != http.StatusNoContent {
		t.Errorf("bob withdrawing his own: %v %d", err, got.StatusCode())
	}
	if got, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{MaxUses: &two}); err != nil || got.JSON201 == nil {
		t.Errorf("the places come back: %v %s", err, got.Body)
	}

	admins := client.WhoCanInvite("admins")
	if _, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{WhoCanInvite: &admins}); err != nil {
		t.Fatal(err)
	}
	if got, err := bob.client.CreateInviteWithResponse(ctx, client.InviteCreate{}); err != nil || got.StatusCode() != http.StatusForbidden {
		t.Errorf("once only admins can: %v %d", err, got.StatusCode())
	}
}

func TestTheAuditLog(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)

	refused, err := bob.client.ListAuditWithResponse(ctx, nil)
	if err != nil || refused.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading it: %v %d", err, refused.StatusCode())
	}

	if _, err := bob.client.CreateAPITokenWithResponse(ctx, client.APITokenCreate{Label: "laptop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.CreateInviteWithResponse(ctx, client.InviteCreate{Name: ptr("alex")}); err != nil {
		t.Fatal(err)
	}
	log, err := f.client.ListAuditWithResponse(ctx, nil)
	if err != nil || log.JSON200 == nil {
		t.Fatalf("read it: %v", err)
	}
	var got []string
	for _, e := range log.JSON200.Items {
		got = append(got, e.Who+" "+e.Action+" "+e.Target)
	}
	if want := "bob api-token.created laptop,admin invite.created alex"; strings.Join(got, ",") != want {
		t.Errorf("audit = %v", got)
	}
	if log.JSON200.Items[0].IP == "" {
		t.Error("no address on an audit line")
	}
}

func TestManagingUsers(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)

	// Only admins.
	if resp, err := bob.client.ListUsersWithResponse(ctx, nil); err != nil || resp.StatusCode() != http.StatusForbidden {
		t.Errorf("a user listing users: %v %d", err, resp.StatusCode())
	}
	role := client.UserPatchRoleAdmin
	if resp, err := bob.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, bob.actor.UserID, client.UserPatch{Role: &role}); err != nil || resp.StatusCode() != http.StatusForbidden {
		t.Errorf("a user making themselves admin: %v %d", err, resp.StatusCode())
	}
	if resp, err := bob.client.DeleteUserWithResponse(ctx, f.actor.UserID); err != nil || resp.StatusCode() != http.StatusForbidden {
		t.Errorf("a user deleting an admin: %v %d", err, resp.StatusCode())
	}

	listed, err := f.client.ListUsersWithResponse(ctx, nil)
	if err != nil || listed.JSON200 == nil || len(listed.JSON200.Items) != 2 {
		t.Fatalf("users = %s, %v", listed.Body, err)
	}

	// The only admin can't stop being one, and nobody deletes themselves.
	user := client.UserPatchRoleUser
	if resp, _ := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, f.actor.UserID, client.UserPatch{Role: &user}); resp.StatusCode() != http.StatusConflict {
		t.Errorf("demoting the last admin: %d", resp.StatusCode())
	}
	yes := true
	if resp, _ := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, f.actor.UserID, client.UserPatch{Disabled: &yes}); resp.StatusCode() != http.StatusConflict {
		t.Errorf("disabling the last admin: %d", resp.StatusCode())
	}
	if resp, _ := f.client.DeleteUserWithResponse(ctx, f.actor.UserID); resp.StatusCode() != http.StatusConflict {
		t.Errorf("deleting yourself: %d", resp.StatusCode())
	}
	if resp, _ := f.client.DeleteUserWithResponse(ctx, 999); resp.StatusCode() != http.StatusNotFound {
		t.Errorf("deleting nobody: %d", resp.StatusCode())
	}
}

func TestDisablingAUserLeavesTheirFeeds(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := f.createSource(t, "s").ID
	f.addEntries(t, s, 3, true)
	bob := f.user(t, "bob", false)
	read, err := bob.client.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: s, Name: "r", Pacing: rrulePacing(t)})
	if err != nil || read.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, read.Body)
	}
	token, err := bob.client.CreateFeedTokenWithResponse(ctx, read.JSON201.ID, client.FeedTokenCreate{Role: client.Viewer, Label: "a friend"})
	if err != nil || token.JSON201 == nil {
		t.Fatalf("create token: %v", err)
	}
	session, err := f.store.CreateSession(ctx, bob.actor.UserID, store.Client{IP: "203.0.113.9"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}

	yes := true
	disabled, err := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, bob.actor.UserID, client.UserPatch{Disabled: &yes})
	if err != nil || disabled.JSON200 == nil || !disabled.JSON200.DisabledAt.IsSpecified() || disabled.JSON200.DisabledAt.IsNull() {
		t.Fatalf("disable: %v %s", err, disabled.Body)
	}
	if me, _ := bob.client.GetMeWithResponse(ctx); me.StatusCode() != http.StatusUnauthorized {
		t.Errorf("a disabled user's API token: %d", me.StatusCode())
	}
	if _, err := f.store.LookupSession(ctx, session, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a disabled user's session: %v", err)
	}
	if _, err := f.store.LookupFeedToken(ctx, token.JSON201.Token); err != nil {
		t.Errorf("a disabled user's feed stopped: %v", err)
	}

	no := false
	if resp, _ := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, bob.actor.UserID, client.UserPatch{Disabled: &no}); resp.StatusCode() != http.StatusOK {
		t.Errorf("enable: %d", resp.StatusCode())
	}
	if me, _ := bob.client.GetMeWithResponse(ctx); me.StatusCode() != http.StatusOK {
		t.Errorf("an enabled user's API token: %d", me.StatusCode())
	}

	// Deleting takes the reads and the feeds with it.
	if resp, _ := f.client.DeleteUserWithResponse(ctx, bob.actor.UserID); resp.StatusCode() != http.StatusNoContent {
		t.Fatalf("delete: %d", resp.StatusCode())
	}
	if _, err := f.store.LookupFeedToken(ctx, token.JSON201.Token); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted user's feed: %v", err)
	}
	if _, err := f.store.GetRead(ctx, store.Holder, read.JSON201.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted user's read: %v", err)
	}
}

func TestARoleChangeEndsSessions(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	session, err := f.store.CreateSession(ctx, bob.actor.UserID, store.Client{IP: "203.0.113.9"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	admin := client.UserPatchRoleAdmin
	if resp, _ := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, bob.actor.UserID, client.UserPatch{Role: &admin}); resp.StatusCode() != http.StatusOK {
		t.Fatalf("promote: %d", resp.StatusCode())
	}
	if _, err := f.store.LookupSession(ctx, session, time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session outlived a role change: %v", err)
	}
	// Two admins now, so one can step down.
	user := client.UserPatchRoleUser
	if resp, _ := f.client.UpdateUserWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, f.actor.UserID, client.UserPatch{Role: &user}); resp.StatusCode() != http.StatusOK {
		t.Errorf("an admin stepping down with another left: %d", resp.StatusCode())
	}
}

func TestSettings(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	bob := f.user(t, "bob", false)
	on := true

	if got, err := bob.client.GetSettingsWithResponse(ctx); err != nil || got.StatusCode() != http.StatusForbidden {
		t.Errorf("a user reading them: %v %d", err, got.StatusCode())
	}
	if got, err := bob.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{RequireTwoFactor: &on}); err != nil || got.StatusCode() != http.StatusForbidden {
		t.Errorf("a user changing them: %v %d", err, got.StatusCode())
	}
	if got, err := f.client.GetSettingsWithResponse(ctx); err != nil || got.JSON200 == nil || got.JSON200.RequireTwoFactor {
		t.Errorf("by default: %v %+v", err, got.JSON200)
	}
	got, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{RequireTwoFactor: &on})
	if err != nil || got.JSON200 == nil || !got.JSON200.RequireTwoFactor {
		t.Fatalf("require two-factor: %v %d", err, got.StatusCode())
	}
	if required, err := f.store.RequireTOTP(ctx); err != nil || !required {
		t.Errorf("stored = %v, %v", required, err)
	}
	if same, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{}); err != nil || same.JSON200 == nil || !same.JSON200.RequireTwoFactor {
		t.Errorf("an empty patch changed them: %v", err)
	}
	if got.JSON200.WhoCanInvite != "admins" || got.JSON200.InviteLimit != 5 {
		t.Errorf("invites by default: %+v", got.JSON200)
	}
	limit := int32(-1)
	if bad, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{InviteLimit: &limit}); err != nil || bad.StatusCode() != http.StatusBadRequest {
		t.Errorf("a negative limit: %v %d", err, bad.StatusCode())
	}

	if got.JSON200.PasswordMinLength != 8 || got.JSON200.PasswordNonLetters != 1 || got.JSON200.PasswordClasses != 0 {
		t.Errorf("passwords by default: %+v", got.JSON200)
	}
	length, nonLetters, classes := int32(20), int32(2), int32(3)
	policy, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx,
		client.SettingsPatch{PasswordMinLength: &length, PasswordNonLetters: &nonLetters, PasswordClasses: &classes})
	if err != nil || policy.JSON200 == nil || policy.JSON200.PasswordMinLength != 20 || policy.JSON200.PasswordNonLetters != 2 || policy.JSON200.PasswordClasses != 3 {
		t.Fatalf("password policy: %v %d", err, policy.StatusCode())
	}
	if stored, err := f.store.GetPasswordPolicy(ctx); err != nil || stored != (password.Policy{MinLength: 20, NonLetters: 2, Classes: 3}) {
		t.Errorf("stored policy = %+v, %v", stored, err)
	}
	for _, patch := range []client.SettingsPatch{{PasswordMinLength: ptr(int32(7))}, {PasswordMinLength: ptr(int32(129))}, {PasswordClasses: ptr(int32(5))}, {PasswordNonLetters: ptr(int32(21))}} {
		if bad, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, patch); err != nil || bad.StatusCode() != http.StatusBadRequest {
			t.Errorf("%+v: %v %d", patch, err, bad.StatusCode())
		}
	}

	if got.JSON200.Theme != "default" || len(got.JSON200.Themes) < 2 || got.JSON200.Themes[0].Name != "default" || got.JSON200.Themes[0].Description == "" {
		t.Errorf("themes by default: %q %+v", got.JSON200.Theme, got.JSON200.Themes)
	}
	paper := "paper"
	themed, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{Theme: &paper})
	if err != nil || themed.JSON200 == nil || themed.JSON200.Theme != "paper" {
		t.Fatalf("theme: %v %s", err, themed.Body)
	}
	if stored, err := f.store.GetTheme(ctx); err != nil || stored != "paper" {
		t.Errorf("stored theme = %q, %v", stored, err)
	}
	for _, name := range []string{"nope", "Not A Name"} {
		if bad, err := f.client.UpdateSettingsWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, client.SettingsPatch{Theme: &name}); err != nil || bad.StatusCode() != http.StatusBadRequest {
			t.Errorf("theme %q: %v %d", name, err, bad.StatusCode())
		}
	}
	if stored, err := f.store.GetTheme(ctx); err != nil || stored != "paper" {
		t.Errorf("theme after refusals = %q, %v", stored, err)
	}
}
