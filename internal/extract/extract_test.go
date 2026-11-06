// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func extractHTML(t *testing.T, html string, overrides Overrides) Result {
	t.Helper()
	extractor, err := For("page")
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractor.Extract(Page{URL: "https://comic.example/dir/page", Body: []byte(html)}, overrides)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func fixture(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sites", path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFixtures(t *testing.T) {
	tests := []struct {
		path, title, image, alt, next, date string
	}{
		{
			path: "comiccontrol/2.html", title: "Harbour Lights - Page 2", image: "https://comic.example/comics/2.png",
			alt: "Hover text 2", next: "https://comic.example/dir/3.html",
		},
		{
			path: "wordpress-webcomic/comic/2/index.html", title: "Strip 2", image: "https://cdn.tidewater.example/strip-2.jpg",
			next: "https://comic.example/comic/3/", date: "2015-02-01T08:00:00Z",
		},
		{
			path: "jsonld-only/2.html", title: "Episode 2", image: "https://comic.example/art/2.webp",
			next: "https://comic.example/dir/3.html", date: "2020-03-02T00:00:00Z",
		},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			f := extractHTML(t, fixture(t, tt.path), Overrides{}).Fields
			if f.Title != tt.title {
				t.Errorf("title = %q, want %q", f.Title, tt.title)
			}
			if !slices.Equal(f.Images, []string{tt.image}) {
				t.Errorf("images = %v, want %s", f.Images, tt.image)
			}
			if f.AltText != tt.alt {
				t.Errorf("alt = %q, want %q", f.AltText, tt.alt)
			}
			if f.Next != tt.next {
				t.Errorf("next = %q, want %q", f.Next, tt.next)
			}
			if tt.date != "" && (f.Date == nil || f.Date.Format(time.RFC3339) != tt.date) {
				t.Errorf("date = %v, want %s", f.Date, tt.date)
			}
		})
	}
}

const layered = `<html><head>
  <title>From the title tag</title>
  <meta property="og:title" content="From Open Graph">
  <meta property="og:image" content="/og.png">
  <link rel="next" href="/rel-next">
  <script type="application/ld+json">
  {"@context":"https://schema.org","@type":"Article","headline":"From JSON-LD","image":{"@type":"ImageObject","url":"https://comic.example/ld.png"},"datePublished":"2011-05-06"}
  </script>
</head><body>
  <img id="cc-comic" src="/platform.png">
  <a class="cc-next" href="/platform-next">Next</a>
  <h1 class="mine">From a selector</h1>
  <img class="mine" src="/selected.png" title="Selected alt">
  <a class="mine" href="/selected-next">On</a>
  <time class="mine" datetime="1999-01-02">then</time>
</body></html>`

func TestPrecedence(t *testing.T) {
	f := extractHTML(t, layered, Overrides{}).Fields
	if f.Title != "From JSON-LD" {
		t.Errorf("title = %q, want JSON-LD's", f.Title)
	}
	if !slices.Equal(f.Images, []string{"https://comic.example/ld.png"}) {
		t.Errorf("images = %v, want JSON-LD's", f.Images)
	}
	if f.Next != "https://comic.example/rel-next" {
		t.Errorf("next = %q, want the link relation over the platform pattern", f.Next)
	}
}

func TestOverridesWin(t *testing.T) {
	f := extractHTML(t, layered, Overrides{
		Title: "h1.mine", Image: "img.mine", AltText: "img.mine", Date: "time.mine", Next: "a.mine",
	}).Fields
	if f.Title != "From a selector" {
		t.Errorf("title = %q", f.Title)
	}
	if !slices.Equal(f.Images, []string{"https://comic.example/selected.png"}) {
		t.Errorf("images = %v", f.Images)
	}
	if f.AltText != "Selected alt" {
		t.Errorf("alt = %q", f.AltText)
	}
	if f.Next != "https://comic.example/selected-next" {
		t.Errorf("next = %q", f.Next)
	}
	if f.Date == nil || f.Date.Year() != 1999 {
		t.Errorf("date = %v", f.Date)
	}
}

func TestJSONLD(t *testing.T) {
	tests := []struct {
		name, block, title, image string
		wantWarning               string
	}{
		{name: "graph", title: "In a graph", image: "https://comic.example/g.png",
			block: `{"@context":"https://schema.org","@graph":[{"@type":"WebSite","name":"Site"},{"@type":"ComicStory","name":"In a graph","image":"https://comic.example/g.png"}]}`},
		{name: "http context", title: "Old context",
			block: `{"@context":"http://schema.org/","@type":"CreativeWork","name":"Old context"}`},
		{name: "ignores other types",
			block: `{"@context":"https://schema.org","@type":"Organization","name":"A Company","image":"https://comic.example/logo.png"}`},
		{name: "invalid JSON", block: `{nope`, wantWarning: "not valid JSON"},
		{name: "remote context is refused", block: `{"@context":"https://attacker.example/context.jsonld","@type":"ComicStory","name":"x"}`, wantWarning: "could not be processed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html := `<html><head><script type="application/ld+json">` + tt.block + `</script></head></html>`
			result := extractHTML(t, html, Overrides{})
			if result.Fields.Title != tt.title {
				t.Errorf("title = %q, want %q", result.Fields.Title, tt.title)
			}
			if tt.image != "" && !slices.Equal(result.Fields.Images, []string{tt.image}) {
				t.Errorf("images = %v", result.Fields.Images)
			}
			if tt.wantWarning != "" && !strings.Contains(strings.Join(result.Warnings, "\n"), tt.wantWarning) {
				t.Errorf("warnings = %v, want %q", result.Warnings, tt.wantWarning)
			}
		})
	}
}

func TestWarnings(t *testing.T) {
	result := extractHTML(t, `<a rel="next" href="/dir/page/">Next</a>`, Overrides{})
	joined := strings.Join(result.Warnings, "\n")
	for _, want := range []string{"no image found", "next link points to the current page"} {
		if !strings.Contains(joined, want) {
			t.Errorf("warnings %q do not contain %q", joined, want)
		}
	}
}

func TestFragmentsAreDropped(t *testing.T) {
	f := extractHTML(t, `<a rel="next" href="/next#content">Next</a>`, Overrides{}).Fields
	if f.Next != "https://comic.example/next" {
		t.Errorf("next = %q", f.Next)
	}
}

func TestKindDispatch(t *testing.T) {
	if _, err := For("page"); err != nil {
		t.Errorf("For(page) = %v", err)
	}
	for _, kind := range []string{"article", "episode"} {
		if _, err := For(kind); err != nil {
			t.Errorf("For(%s) = %v", kind, err)
		}
	}
	if _, err := For("song"); err == nil {
		t.Error("For(song) found an extractor")
	}
}

func TestArticle(t *testing.T) {
	long := strings.Repeat("Enough text to count as the main part of an article. ", 20)
	page := Page{URL: "https://blog.example/2/", Body: []byte(`<!doctype html><html><head><title>A Post | Blog</title>
<meta property="og:title" content="A Post"><meta property="og:description" content="About it">
</head><body><nav>Menu</nav><article><h1>A Post</h1><p onclick="x()">` + long + `</p><script>x()</script></article>
<nav><div class="nav-next"><a href="/3/">Next</a></div></nav></body></html>`)}
	extractor, err := For("article")
	if err != nil {
		t.Fatal(err)
	}

	off, err := extractor.Extract(page, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if off.Fields.Title != "A Post" || off.Fields.Next != "https://blog.example/3/" || off.Fields.ContentHTML != "" {
		t.Errorf("fields = %+v", off.Fields)
	}

	on, err := extractor.Extract(page, Overrides{FullText: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(on.Fields.ContentHTML, "main part of an article") {
		t.Errorf("content = %q", on.Fields.ContentHTML)
	}
	if strings.Contains(on.Fields.ContentHTML, "<script") || strings.Contains(on.Fields.ContentHTML, "onclick") {
		t.Errorf("content was not sanitised: %q", on.Fields.ContentHTML)
	}
}

func TestSanitise(t *testing.T) {
	got := Sanitise(`<p onclick="x()">ok</p><script>x()</script><iframe src="https://e.example/"></iframe><a href="javascript:x()">l</a>`)
	for _, bad := range []string{"onclick", "<script", "<iframe", "javascript:"} {
		if strings.Contains(got, bad) {
			t.Errorf("kept %s: %q", bad, got)
		}
	}
	if !strings.Contains(got, "ok") {
		t.Errorf("lost the text: %q", got)
	}
}

func TestEpisode(t *testing.T) {
	extractor, err := For("episode")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, body, want, wantType string
	}{
		{"audio tag", `<title>t</title><audio src="/a.mp3"></audio>`, "https://pod.example/a.mp3", ""},
		{"source tag", `<title>t</title><audio><source src="/b.ogg" type="audio/ogg"></audio>`, "https://pod.example/b.ogg", "audio/ogg"},
		{"video tag", `<title>t</title><video src="/c.mp4"></video>`, "https://pod.example/c.mp4", ""},
		{"JSON-LD", `<title>t</title><script type="application/ld+json">{"@context":"https://schema.org","@type":"PodcastEpisode","name":"t","associatedMedia":{"@type":"MediaObject","contentUrl":"https://cdn.example/d.mp3"}}</script>`, "https://cdn.example/d.mp3", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := extractor.Extract(Page{URL: "https://pod.example/1", Body: []byte(tt.body)}, Overrides{})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Fields.Enclosures) != 1 || result.Fields.Enclosures[0].URL != tt.want || result.Fields.Enclosures[0].Type != tt.wantType {
				t.Errorf("enclosures = %+v", result.Fields.Enclosures)
			}
		})
	}
	t.Run("nothing to play", func(t *testing.T) {
		result, err := extractor.Extract(Page{URL: "https://pod.example/1", Body: []byte(`<title>t</title><p>words</p>`)}, Overrides{})
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Warnings) == 0 {
			t.Error("no warning for a page with no media")
		}
	})
}
