// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/mmcdole/gofeed"
)

const adminToken = "e2e-admin-token"

var csrfField = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// rig is one real server and the database that outlives its restarts. Time moves
// by restarting it further ahead, so nothing sleeps.
type rig struct {
	bin, dsn         string
	catalogueBin     string
	catalogueDSN     string
	catalogueProcess *process
	process          *process
	api              *client.ClientWithResponses
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{
		bin: buildServer(t), dsn: storetest.DSN(t),
		catalogueBin: build(t, "andante-catalogue"), catalogueDSN: storetest.DSN(t),
	}
	r.startCatalogue(t, "")
	r.start(t, 0)
	return r
}

// startCatalogue starts the catalogue, on the same address as last time if addr
// says so, because the server has it in its flags.
func (r *rig) startCatalogue(t *testing.T, addr string) {
	t.Helper()
	flags := crawlFlags
	if addr != "" {
		flags = append([]string{"--listen=" + addr}, crawlFlags...)
	}
	r.catalogueProcess = startCatalogue(t, r.catalogueBin, r.catalogueDSN, flags...)
	if addr != "" {
		r.catalogueProcess.admin = addr
	}
	r.catalogueProcess.waitReady(t)
}

func stop(t *testing.T, p *process) {
	t.Helper()
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := <-p.waited; err != nil {
		t.Fatalf("shutdown: %v\n%s", err, p.output())
	}
}

func (r *rig) start(t *testing.T, ahead time.Duration) {
	t.Helper()
	flags := []string{
		"--admin-token=" + adminToken, "--feed-rate-limit=100000", "--test-clock-offset=" + ahead.String(),
		r.catalogueProcess.flag(), "--demand-interval=50ms",
	}
	r.process = startServer(t, r.bin, r.dsn, flags...)
	r.process.waitReady(t)
	api, err := client.NewClientWithResponses("http://"+r.process.admin+"/api/v1",
		client.WithRequestEditorFn(func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+adminToken)
			return nil
		}))
	if err != nil {
		t.Fatal(err)
	}
	r.api = api
}

func (r *rig) restart(t *testing.T, ahead time.Duration) {
	t.Helper()
	stop(t, r.process)
	r.start(t, ahead)
}

func (r *rig) waitForTail(t *testing.T, source int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := r.api.GetSourceWithResponse(t.Context(), source)
		if err != nil {
			t.Fatal(err)
		}
		if got.JSON200 != nil && got.JSON200.State == client.SourceStateTail {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("source %d never reached tail. Logs:\n%s", source, r.process.output())
}

func (r *rig) token(t *testing.T, read int64, role client.Role) *client.IssuedFeedToken {
	t.Helper()
	resp, err := r.api.CreateFeedTokenWithResponse(t.Context(), read, client.FeedTokenCreate{Role: role, Label: string(role)})
	if err != nil || resp.JSON201 == nil {
		t.Fatalf("create token: %v %v", err, resp)
	}
	return resp.JSON201
}

func (r *rig) feed(t *testing.T, token string) *gofeed.Feed {
	t.Helper()
	resp, err := http.Get("http://" + r.process.public + "/f/" + token + ".rss")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("feed: status %d", resp.StatusCode)
	}
	parsed, err := gofeed.NewParser().Parse(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func (r *rig) position(t *testing.T, read int64) int32 {
	t.Helper()
	resp, err := r.api.GetReadWithResponse(t.Context(), read)
	if err != nil || resp.JSON200 == nil {
		t.Fatalf("get read: %v", err)
	}
	return resp.JSON200.Position
}

func (r *rig) adjust(t *testing.T, read int64, build func(*client.Adjustment) error) {
	t.Helper()
	var adjustment client.Adjustment
	if err := build(&adjustment); err != nil {
		t.Fatal(err)
	}
	resp, err := r.api.CreateAdjustmentWithResponse(t.Context(), read, adjustment)
	if err != nil || resp.StatusCode() >= 300 {
		t.Fatalf("adjust: %v %s", err, resp.Body)
	}
}

// next presses the owner's link: the confirmation page, then the form.
func (r *rig) next(t *testing.T, token string) string {
	t.Helper()
	page := "http://" + r.process.public + "/f/" + token + "/next"
	resp, err := http.Get(page)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var html strings.Builder
	if _, err := io.Copy(&html, resp.Body); err != nil {
		t.Fatal(err)
	}
	match := csrfField.FindStringSubmatch(html.String())
	if match == nil {
		t.Fatalf("no CSRF field: %d %s", resp.StatusCode, html.String())
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	posted, err := noRedirect.PostForm(page, url.Values{"csrf": {match[1]}})
	if err != nil {
		t.Fatal(err)
	}
	defer posted.Body.Close()
	if posted.StatusCode != http.StatusSeeOther {
		t.Fatalf("next: status %d", posted.StatusCode)
	}
	return posted.Header.Get("Location")
}

func ids(f *gofeed.Feed) []string {
	out := make([]string, 0, len(f.Items))
	for _, item := range f.Items {
		out = append(out, item.GUID)
	}
	slices.Sort(out)
	return out
}

func TestComicFromProbeToRevokedToken(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	site := sitetest.Serve(t, "comiccontrol")

	var config client.SourceConfig
	if err := config.FromCrawlConfig(client.CrawlConfig{Strategy: client.Crawl, StartURL: site.URL + "/1.html"}); err != nil {
		t.Fatal(err)
	}
	limit := int32(3)
	probe, err := r.api.CreateProbeWithResponse(ctx, client.ProbeRequest{Config: config, Limit: &limit})
	if err != nil || probe.JSON200 == nil {
		t.Fatalf("probe: %v %s", err, probe.Body)
	}
	if len(probe.JSON200.Entries) != 3 {
		t.Fatalf("probe entries = %d", len(probe.JSON200.Entries))
	}

	source, err := r.api.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "comic", Title: "Comic", HomeURL: site.URL, DefaultKind: client.Page, Config: config,
	})
	if err != nil || source.JSON201 == nil {
		t.Fatalf("create source: %v %s", err, source.Body)
	}
	r.waitForTail(t, source.JSON201.ID)

	var pacing client.Pacing
	if err := pacing.FromRRulePacing(client.RRulePacing{Mode: client.Rrule, Rrule: "FREQ=DAILY", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	created, err := r.api.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: source.JSON201.ID, Name: "Mornings", Pacing: pacing})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, created.Body)
	}
	read := created.JSON201.ID
	owner, viewer := r.token(t, read, client.Owner), r.token(t, read, client.Viewer)

	first := ids(r.feed(t, viewer.Token))
	if len(first) != 1 {
		t.Fatalf("items at the start = %v", first)
	}
	if got := ids(r.feed(t, owner.Token)); !slices.Equal(got, first) {
		t.Errorf("owner's items %v differ from the viewer's %v", got, first)
	}

	location := r.next(t, owner.Token)
	if !strings.Contains(location, "/2.html") {
		t.Errorf("next went to %q", location)
	}
	two := ids(r.feed(t, viewer.Token))
	if len(two) != 2 {
		t.Fatalf("items after next = %v", two)
	}

	// Paused, a read stays put however much time passes, through a restart.
	r.adjust(t, read, func(a *client.Adjustment) error {
		return a.FromPauseAdjustment(client.PauseAdjustment{Type: client.PauseAdjustmentTypePause})
	})
	position := r.position(t, read)
	r.restart(t, 72*time.Hour)
	if got := r.position(t, read); got != position {
		t.Errorf("position moved while paused: %d, was %d", got, position)
	}
	if got := ids(r.feed(t, viewer.Token)); !slices.Equal(got, two) {
		t.Errorf("items changed across a restart: %v, were %v", got, two)
	}

	r.adjust(t, read, func(a *client.Adjustment) error {
		return a.FromResumeAdjustment(client.ResumeAdjustment{Type: client.ResumeAdjustmentTypeResume})
	})
	r.restart(t, 72*time.Hour+25*time.Hour)
	if got := r.position(t, read); got != position+1 {
		t.Errorf("position a day after resuming = %d, want %d", got, position+1)
	}
	three := ids(r.feed(t, viewer.Token))
	if len(three) != 3 {
		t.Fatalf("items a day after resuming = %v", three)
	}
	for _, id := range two {
		if !slices.Contains(three, id) {
			t.Errorf("item %q changed its ID", id)
		}
	}

	revoked, err := r.api.RevokeFeedTokenWithResponse(ctx, viewer.ID)
	if err != nil || revoked.StatusCode() >= 300 {
		t.Fatalf("revoke: %v", err)
	}
	resp, err := http.Get("http://" + r.process.public + "/f/" + viewer.Token + ".rss")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("revoked token: status %d", resp.StatusCode)
	}
	if got := ids(r.feed(t, owner.Token)); !slices.Equal(got, three) {
		t.Errorf("revoking one token changed the owner's feed: %v", got)
	}
}

func TestPodcastReplayedSevenTimesFaster(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	site := sitetest.Serve(t, "podcast")

	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: site.URL + "/feed.xml"}); err != nil {
		t.Fatal(err)
	}
	source, err := r.api.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "podcast", Title: "A Podcast", HomeURL: site.URL, DefaultKind: client.Episode, Config: config,
	})
	if err != nil || source.JSON201 == nil {
		t.Fatalf("create source: %v %s", err, source.Body)
	}
	r.waitForTail(t, source.JSON201.ID)

	var pacing client.Pacing
	if err := pacing.FromReplayPacing(client.ReplayPacing{Mode: client.Replay, Speed: 7}); err != nil {
		t.Fatal(err)
	}
	created, err := r.api.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: source.JSON201.ID, Name: "Quick", Pacing: pacing})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, created.Body)
	}
	start := created.JSON201.CreatedAt
	token := r.token(t, created.JSON201.ID, client.Viewer).Token

	// The show was weekly, so at 7x each episode comes a day after the last.
	for day := range 5 {
		r.restart(t, time.Duration(day)*24*time.Hour+time.Minute)
		items := r.feed(t, token).Items
		if len(items) != day+1 {
			t.Fatalf("day %d: %d items, want %d", day, len(items), day+1)
		}
		newest := items[0]
		if want := fmt.Sprintf("Episode %d", day+1); newest.Title != want {
			t.Errorf("day %d: newest = %q, want %q", day, newest.Title, want)
		}
		if newest.PublishedParsed == nil {
			t.Fatalf("day %d: no pubDate", day)
		}
		// An item is dated by its release, not by the original run.
		if drift := newest.PublishedParsed.Sub(start) - time.Duration(day)*24*time.Hour; drift.Abs() > time.Minute {
			t.Errorf("day %d: released at %s, %s from when it was due", day, newest.PublishedParsed, drift)
		}
		want := fmt.Sprintf("https://cdn.podcast.example/%d.mp3", day+1)
		if len(newest.Enclosures) != 1 || newest.Enclosures[0].URL != want || newest.Enclosures[0].Type != "audio/mpeg" {
			t.Errorf("day %d: enclosures = %+v", day, newest.Enclosures)
		}
	}
}

func TestOPMLImportThenProbeThenEnable(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	site := sitetest.Serve(t, "podcast")
	opml := `<opml version="2.0"><body><outline text="Shows"><outline title="A Podcast" xmlUrl="` + site.URL + `/feed.xml"/></outline></body></opml>`

	imported, err := r.api.ImportOPMLWithBodyWithResponse(ctx, "text/x-opml", strings.NewReader(opml))
	if err != nil || imported.JSON200 == nil {
		t.Fatalf("import: %v %s", err, imported.Body)
	}
	result := imported.JSON200.Results[0]
	if result.Status != client.OPMLOutlineResultStatusCreated || result.SourceID == nil {
		t.Fatalf("result = %+v", result)
	}
	id := *result.SourceID

	source, err := r.api.GetSourceWithResponse(ctx, id)
	if err != nil || source.JSON200 == nil {
		t.Fatalf("get source: %v", err)
	}
	if source.JSON200.State != client.SourceStatePaused {
		t.Fatalf("state = %q, want paused", source.JSON200.State)
	}
	if got := site.Count("/feed.xml"); got != 0 {
		t.Errorf("an import fetched the feed %d times", got)
	}

	probe, err := r.api.CreateProbeWithResponse(ctx, client.ProbeRequest{Config: source.JSON200.Config})
	if err != nil || probe.JSON200 == nil {
		t.Fatalf("probe: %v %s", err, probe.Body)
	}
	if len(probe.JSON200.Entries) == 0 {
		t.Fatal("the probe found nothing")
	}

	state := client.SourcePatchStateIndexing
	enabled, err := r.api.UpdateSourceWithApplicationMergePatchPlusJSONBodyWithResponse(ctx, id, client.SourcePatch{State: &state})
	if err != nil || enabled.JSON200 == nil {
		t.Fatalf("enable: %v %s", err, enabled.Body)
	}
	r.waitForTail(t, id)
	entries, err := r.api.ListEntriesWithResponse(ctx, id, nil)
	if err != nil || entries.JSON200 == nil {
		t.Fatalf("entries: %v", err)
	}
	if got := len(entries.JSON200.Items); got != 5 {
		t.Errorf("entries = %d, want 5", got)
	}

	again, err := r.api.ImportOPMLWithBodyWithResponse(ctx, "text/x-opml", strings.NewReader(opml))
	if err != nil || again.JSON200 == nil {
		t.Fatalf("second import: %v", err)
	}
	if got := again.JSON200.Results[0].Status; got != client.OPMLOutlineResultStatusDuplicate {
		t.Errorf("second import = %q, want duplicate", got)
	}
}

// The point of the copy: with the catalogue gone, feeds carry on, and when it comes
// back what it found reaches them.
func TestFeedsOutliveTheCatalogue(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	site := sitetest.Serve(t, "podcast")

	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: site.URL + "/feed.xml"}); err != nil {
		t.Fatal(err)
	}
	source, err := r.api.CreateSourceWithResponse(ctx, client.SourceCreate{
		Slug: "podcast", Title: "A Podcast", HomeURL: site.URL, DefaultKind: client.Episode, Config: config,
	})
	if err != nil || source.JSON201 == nil {
		t.Fatalf("create source: %v %s", err, source.Body)
	}
	id := source.JSON201.ID
	r.waitForTail(t, id)

	var pacing client.Pacing
	if err := pacing.FromRRulePacing(client.RRulePacing{Mode: client.Rrule, Rrule: "FREQ=DAILY", Timezone: "UTC"}); err != nil {
		t.Fatal(err)
	}
	created, err := r.api.CreateReadWithResponse(ctx, client.ReadCreate{SourceID: id, Name: "Daily", Pacing: pacing})
	if err != nil || created.JSON201 == nil {
		t.Fatalf("create read: %v %s", err, created.Body)
	}
	token := r.token(t, created.JSON201.ID, client.Viewer).Token

	// The server has it in its flags, so the catalogue comes back on the same address.
	addr := r.catalogueProcess.admin
	stop(t, r.catalogueProcess)

	if got := len(r.feed(t, token).Items); got != 1 {
		t.Fatalf("items with the catalogue down = %d, want 1", got)
	}
	got, err := r.api.GetSourceWithResponse(ctx, id)
	if err != nil || got.JSON200 == nil {
		t.Errorf("get source with the catalogue down: %v", err)
	}
	refused, err := r.api.RequestCrawlWithResponse(ctx, id)
	if err != nil || refused.StatusCode() != http.StatusServiceUnavailable {
		t.Errorf("crawl with the catalogue down: %v %d", err, refused.StatusCode())
	}

	// A release still happens with the catalogue gone, and through a restart.
	r.restart(t, 25*time.Hour)
	if got := len(r.feed(t, token).Items); got != 2 {
		t.Fatalf("items a day on with the catalogue down = %d, want 2", got)
	}

	feed := `<rss version="2.0"><channel><title>A Podcast</title><item><title>Episode 6</title><guid>six</guid>
<enclosure url="https://cdn.podcast.example/6.mp3" length="1" type="audio/mpeg"/>
<pubDate>Mon, 10 Feb 2020 08:00:00 +0000</pubDate></item></channel></rss>`
	site.SetPage("/feed.xml", feed)
	r.startCatalogue(t, addr)
	accepted, err := r.api.RequestCrawlWithResponse(ctx, id)
	if err != nil || accepted.StatusCode() != http.StatusAccepted {
		t.Fatalf("crawl: %v %d", err, accepted.StatusCode())
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		entries, err := r.api.ListEntriesWithResponse(ctx, id, nil)
		if err == nil && entries.JSON200 != nil && len(entries.JSON200.Items) == 6 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the new episode never arrived. Logs:\n%s\n%s", r.process.output(), r.catalogueProcess.output())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
