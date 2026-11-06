// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package feed

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/mmcdole/gofeed"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const (
	baseURL = "https://andante.example/"
	raw     = "RAWTOKEN"
)

var released = time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC)

func ptr[T any](v T) *T { return &v }

func read() db.Read {
	return db.Read{ID: 4, Name: "Mornings", Config: []byte("{}"), CreatedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)}
}

func source(kind string) db.Source {
	s := db.Source{
		ID: 1, Title: "Harbour & Lights", HomeUrl: "https://publisher.example/", DefaultKind: kind,
		ImageUrl: ptr("https://publisher.example/logo.png"), Extra: []byte("{}"),
	}
	if kind == kindEpisode {
		s.Extra = []byte(`{"itunes:author":"A Person","itunes:category":["Comedy","Arts"],"itunes:explicit":"true","podcast:guid":"original-show-guid"}`)
	}
	return s
}

func entriesOf(kind string) []reads.ReleasedEntry {
	base := db.Entry{
		ID: 11, Seq: 1, Kind: kind, Url: ptr("https://publisher.example/2"), Title: ptr("Second <one>"),
		Enclosures: []byte("[]"), Extra: []byte("{}"), OriginalDate: ptr(time.Date(2009, 3, 8, 0, 0, 0, 0, time.UTC)),
	}
	older := db.Entry{
		ID: 10, Seq: 0, Kind: kind, Enclosures: []byte("[]"), Extra: []byte("{}"),
	}
	switch kind {
	case kindPage:
		base.Images = []string{"https://publisher.example/2.png"}
		base.AltText = ptr("A joke & its explanation")
		older.Images = []string{"https://publisher.example/1.png"}
	case kindEpisode:
		base.Enclosures = []byte(`[{"url":"https://cdn.publisher.example/2.mp3","type":"audio/mpeg","length":12345}]`)
		base.Duration = pgtype.Interval{Microseconds: 1800 * 1e6, Valid: true}
		base.Summary = ptr("Show notes")
		base.Extra = []byte(`{"itunes:image":"https://publisher.example/2.jpg","itunes:episode":"2","itunes:season":"1","guid":"original-guid","podcast:transcript":{"url":"https://publisher.example/2.vtt","type":"text/vtt"},"podcast:chapters":{"url":"https://publisher.example/2.json","type":"application/json+chapters"}}`)
		older.Enclosures = []byte(`[{"url":"https://cdn.publisher.example/1.mp3","type":"audio/mpeg","length":999}]`)
	case kindArticle:
		base.ContentHtml = ptr("<p>The whole article.</p>")
		older.Summary = ptr("Only a summary")
		older.Url = ptr("https://publisher.example/1")
	}
	return []reads.ReleasedEntry{{Entry: base, At: released}, {Entry: older, At: released.AddDate(0, 0, -1)}}
}

func build(t *testing.T, kind, role string) Feed {
	t.Helper()
	f, err := Build(Input{
		Read: read(), Source: source(kind), Entries: entriesOf(kind), Role: role, Token: raw, BaseURL: baseURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.Version = "v0.0.0-test" // The golden files should not change with the build.
	return f
}

func TestGolden(t *testing.T) {
	for _, format := range []string{"atom", "rss", "json"} {
		for _, role := range []string{RoleOwner, RoleViewer} {
			for _, kind := range []string{kindPage, kindEpisode, kindArticle} {
				t.Run(format+"/"+role+"/"+kind, func(t *testing.T) {
					got := render(t, format, build(t, kind, role))
					path := filepath.Join("testdata", "golden", fmt.Sprintf("%s-%s.%s", kind, role, format))
					if *update {
						if err := os.WriteFile(path, got, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					want, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if string(got) != string(want) {
						t.Errorf("output differs from %s. Run with -update and read the diff.\n%s", path, got)
					}
				})
			}
		}
	}
}

func render(t *testing.T, format string, f Feed) []byte {
	t.Helper()
	spec, ok := FormatFor(format)
	if !ok {
		t.Fatalf("no format %s", format)
	}
	body, err := spec.Render(f, URL(baseURL, raw, format))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestRoundTrip(t *testing.T) {
	for _, format := range []string{"atom", "rss", "json"} {
		for _, kind := range []string{kindPage, kindEpisode, kindArticle} {
			t.Run(format+"/"+kind, func(t *testing.T) {
				f := build(t, kind, RoleViewer)
				parsed, err := gofeed.NewParser().ParseString(string(render(t, format, f)))
				if err != nil {
					t.Fatal(err)
				}
				if parsed.Title != f.Title {
					t.Errorf("title = %q", parsed.Title)
				}
				if len(parsed.Items) != 2 {
					t.Fatalf("items = %d", len(parsed.Items))
				}
				item := parsed.Items[0]
				if item.GUID != f.Items[0].ID {
					t.Errorf("id = %q, want %q", item.GUID, f.Items[0].ID)
				}
				if item.Title != "Second <one>" {
					t.Errorf("item title = %q", item.Title)
				}
				if item.PublishedParsed == nil || !item.PublishedParsed.Equal(released) {
					t.Errorf("published = %v, want the release time", item.PublishedParsed)
				}
				if kind == kindEpisode {
					if len(item.Enclosures) != 1 || item.Enclosures[0].URL != "https://cdn.publisher.example/2.mp3" {
						t.Errorf("enclosures = %+v", item.Enclosures)
					}
				}
			})
		}
	}
}

func TestPodcastApps(t *testing.T) {
	body := render(t, "rss", build(t, kindEpisode, RoleViewer))
	parsed, err := gofeed.NewParser().ParseString(string(body))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.ITunesExt == nil || parsed.ITunesExt.Author != "A Person" || parsed.ITunesExt.Image == "" {
		t.Errorf("channel: %+v", parsed.ITunesExt)
	}
	item := parsed.Items[0]
	if item.ITunesExt == nil || item.ITunesExt.Duration != "1800" || item.ITunesExt.Image != "https://publisher.example/2.jpg" {
		t.Errorf("item: %+v", item.ITunesExt)
	}
	e := item.Enclosures[0]
	if e.Type != "audio/mpeg" || e.Length != "12345" {
		t.Errorf("enclosure = %+v", e)
	}
	for _, want := range []string{"podcast:transcript", "podcast:chapters", `isPermaLink="false"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("no %s", want)
		}
	}
	for _, forbidden := range []string{"original-show-guid", "original-guid"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("the publisher's %s was copied", forbidden)
		}
	}
}

func TestViewerOutputNeverContainsAnOwnerURL(t *testing.T) {
	for _, format := range []string{"atom", "rss", "json"} {
		for _, kind := range []string{kindPage, kindEpisode, kindArticle} {
			viewer := string(render(t, format, build(t, kind, RoleViewer)))
			// The feed's self link holds the viewer's own token. An owner link does not exist.
			if strings.Contains(viewer, "/next") {
				t.Errorf("%s %s: a viewer feed has a control", format, kind)
			}
			owner := string(render(t, format, build(t, kind, RoleOwner)))
			if !strings.Contains(owner, "/f/"+raw+"/next") {
				t.Errorf("%s %s: an owner feed has no control", format, kind)
			}
		}
	}
}

func TestXMLIsWellFormed(t *testing.T) {
	for _, format := range []string{"atom", "rss"} {
		var v any
		if err := xml.Unmarshal(render(t, format, build(t, kindArticle, RoleOwner)), &v); err != nil && !strings.Contains(err.Error(), "unknown") {
			t.Errorf("%s: %v", format, err)
		}
	}
}

func TestJSONFeed(t *testing.T) {
	var doc map[string]any
	if err := json.Unmarshal(render(t, "json", build(t, kindEpisode, RoleViewer)), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["version"] != "https://jsonfeed.org/version/1.1" {
		t.Errorf("version = %v", doc["version"])
	}
	items, _ := doc["items"].([]any)
	first, _ := items[0].(map[string]any)
	attachments, _ := first["attachments"].([]any)
	if len(attachments) != 1 {
		t.Fatalf("attachments = %v", first["attachments"])
	}
	a, _ := attachments[0].(map[string]any)
	if a["url"] != "https://cdn.publisher.example/2.mp3" || a["mime_type"] != "audio/mpeg" {
		t.Errorf("attachment = %v", a)
	}
}

func TestItemFields(t *testing.T) {
	f := build(t, kindPage, RoleViewer)
	if want := "tag:andante.example,2026:read/4/entry/11"; f.Items[0].ID != want {
		t.Errorf("id = %q, want %q", f.Items[0].ID, want)
	}
	if want := "Harbour & Lights #0"; f.Items[1].Title != want {
		t.Errorf("fallback title = %q, want %q", f.Items[1].Title, want)
	}
	if !f.Updated.Equal(released) {
		t.Errorf("updated = %s, want the latest release", f.Updated)
	}
	content := f.Items[0].ContentHTML
	for _, want := range []string{"A joke &amp; its explanation", "Originally published 8 March 2009", "https://publisher.example/2.png"} {
		if !strings.Contains(content, want) {
			t.Errorf("content %q lacks %q", content, want)
		}
	}
	if !strings.Contains(f.Description, "paced replay") {
		t.Errorf("description = %q", f.Description)
	}
}

func TestAnEmptyFeed(t *testing.T) {
	f, err := Build(Input{Read: read(), Source: source(kindPage), Role: RoleViewer, BaseURL: baseURL})
	if err != nil {
		t.Fatal(err)
	}
	if !f.Updated.Equal(read().CreatedAt) {
		t.Errorf("updated = %s", f.Updated)
	}
	for _, format := range []string{"atom", "rss", "json"} {
		if _, err := gofeed.NewParser().ParseString(string(render(t, format, f))); err != nil {
			t.Errorf("%s: %v", format, err)
		}
	}
}

func TestBadBaseURL(t *testing.T) {
	if _, err := Build(Input{Read: read(), Source: source(kindPage), BaseURL: "http://[::1"}); err == nil {
		t.Error("Build accepted a bad base URL")
	}
}

func TestWindow(t *testing.T) {
	tests := map[string]int{
		"{}":             DefaultWindow,
		`{"window":5}`:   5,
		`{"window":0}`:   DefaultWindow,
		`{"window":999}`: MaxWindow,
		"nope":           DefaultWindow,
	}
	for config, want := range tests {
		if got := Window(db.Read{Config: []byte(config)}); got != want {
			t.Errorf("Window(%s) = %d, want %d", config, got, want)
		}
	}
}

func TestSuggested(t *testing.T) {
	if Suggested(kindEpisode) != "rss" || Suggested(kindPage) != "atom" || Suggested(kindArticle) != "atom" {
		t.Error("wrong suggestion")
	}
}

func TestOPML(t *testing.T) {
	body, err := OPML([]Outline{{Title: "A & B", FeedURL: "https://a.example/f.atom", HomeURL: "https://a.example/"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`version="2.0"`, `text="A &amp; B"`, `xmlUrl="https://a.example/f.atom"`, `type="rss"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("no %s in %s", want, body)
		}
	}
}

func TestBadExtraIsDropped(t *testing.T) {
	in := Input{Read: read(), Source: source(kindEpisode), Role: RoleViewer, BaseURL: baseURL, Entries: []reads.ReleasedEntry{{
		Entry: db.Entry{ID: 1, Kind: kindEpisode, Enclosures: []byte("not json"), Extra: []byte("not json")}, At: released,
	}}}
	f, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Items) != 1 || len(f.Items[0].Enclosures) != 0 {
		t.Errorf("items = %+v", f.Items)
	}
}
