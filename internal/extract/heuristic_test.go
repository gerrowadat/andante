// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package extract

import (
	"slices"
	"testing"
)

func TestNextLinkByLabel(t *testing.T) {
	tests := []struct {
		name, body, next string
	}{
		{name: "text", body: `<a href="/2">Next »</a>`, next: "https://comic.example/2"},
		{name: "longer text", body: `<a href="/2">NEXT  Comic</a>`, next: "https://comic.example/2"},
		{name: "title", body: `<a href="/2" title="Next page">›</a>`, next: "https://comic.example/2"},
		{name: "aria-label", body: `<a href="/2" aria-label="next"></a>`, next: "https://comic.example/2"},
		{name: "button image", body: `<a href="2"><img src="/b/n.png" alt="Next"></a>`, next: "https://comic.example/dir/2"},
		{name: "text over image", body: `<a href="/2">Older<img src="/b/n.png" alt="Next"></a>`},
		{name: "arrow alone", body: `<a href="/2">»</a>`},
		{name: "sentence", body: `<a href="/2">Next week: the finale</a>`},
		{name: "another site", body: `<a href="https://elsewhere.example/2">Next</a>`},
		{name: "fragment", body: `<a href="#next">Next</a>`},
		{name: "script", body: `<a href="javascript:next()">Next</a>`},
		{name: "first that fits", body: `<a href="#">Next</a><a href="/2">Next</a><a href="/3">Next</a>`, next: "https://comic.example/2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := extractHTML(t, `<html><body>`+tt.body+`</body></html>`, Overrides{}).Fields
			if f.Next != tt.next {
				t.Errorf("next = %q, want %q", f.Next, tt.next)
			}
		})
	}
}

func TestHeuristicsComeLast(t *testing.T) {
	f := extractHTML(t, `<html><body>
		<a href="/labelled">Next</a><a class="cc-next" href="/platform">On</a>
		<img src="/comics/heuristic.png"><img id="cc-comic" src="/platform.png">
	</body></html>`, Overrides{}).Fields
	if f.Next != "https://comic.example/platform" {
		t.Errorf("next = %q, want the platform's", f.Next)
	}
	if !slices.Equal(f.Images, []string{"https://comic.example/platform.png"}) {
		t.Errorf("images = %v, want the platform's", f.Images)
	}
}

func TestMainImage(t *testing.T) {
	tests := []struct {
		name, body, image string
	}{
		{name: "class", body: `<img src="/a.png"><img class="comic_image" src="/b.png">`, image: "https://comic.example/b.png"},
		{name: "id", body: `<img id="theComic" src="/b.png">`, image: "https://comic.example/b.png"},
		{name: "path", body: `<img src="/strips/b.png"><img src="/webcomics/c.png?v=2">`, image: "https://comic.example/webcomics/c.png?v=2"},
		{name: "host is not path", body: `<img src="https://comic.example/b.png">`},
		{name: "header", body: `<header><img src="/comic-logo.png"></header>`},
		{name: "footer", body: `<footer><img src="/comic-footer.png"></footer>`},
		{name: "nav", body: `<nav><img src="/comic-nav.png"></nav>`},
		{name: "button", body: `<a href="/2"><img src="/comics/next.png" alt="Next"></a><img src="/comics/1.png">`, image: "https://comic.example/comics/1.png"},
		{name: "linked to the next page", body: `<a href="/2"><img src="/comics/1.png" alt="Page 1"></a>`, image: "https://comic.example/comics/1.png"},
		{name: "inline", body: `<img class="comic" src="data:image/png;base64,AAAA">`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := extractHTML(t, `<html><body>`+tt.body+`</body></html>`, Overrides{}).Fields
			var want []string
			if tt.image != "" {
				want = []string{tt.image}
			}
			if !slices.Equal(f.Images, want) {
				t.Errorf("images = %v, want %v", f.Images, want)
			}
		})
	}
}

func TestOnlyPagesGuessTheImage(t *testing.T) {
	extractor, err := For("article")
	if err != nil {
		t.Fatal(err)
	}
	result, err := extractor.Extract(Page{URL: "https://blog.example/post", Body: []byte(
		`<html><body><img class="comic" src="/c.png"><a href="/older">Next post</a></body></html>`)}, Overrides{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Fields.Images) != 0 {
		t.Errorf("images = %v", result.Fields.Images)
	}
	if result.Fields.Next != "https://blog.example/older" {
		t.Errorf("next = %q", result.Fields.Next)
	}
}

func TestFirstPageByLabel(t *testing.T) {
	site, err := ReadSite(Page{URL: "https://comic.example/", Body: []byte(fixture(t, "labelled-nav/index.html"))})
	if err != nil {
		t.Fatal(err)
	}
	if site.First != "https://comic.example/1.html" {
		t.Errorf("first = %q", site.First)
	}
}
