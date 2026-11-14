// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type vendored struct {
	Files []struct {
		Path, URL, SHA256 string
	}
}

// A vendored file is exactly what vendor.json says it is, so nobody edits one by
// hand, and nothing sits in vendor/ that vendor.json doesn't account for.
func TestVendoredFilesMatchTheirHashes(t *testing.T) {
	raw, err := staticFiles.ReadFile("static/vendor.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vendored
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, f := range v.Files {
		listed["static/"+f.Path] = true
		body, err := staticFiles.ReadFile("static/" + f.Path)
		if err != nil {
			t.Errorf("%s: %v", f.Path, err)
			continue
		}
		if sum := sha256.Sum256(body); hex.EncodeToString(sum[:]) != f.SHA256 {
			t.Errorf("%s doesn't match its hash in vendor.json: run make vendor-web", f.Path)
		}
	}
	err = fs.WalkDir(staticFiles, "static/vendor", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !listed[path] {
			t.Errorf("%s isn't in vendor.json", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Every stylesheet and script a page links to is there, so upgrading a library and
// forgetting a template fails here and not in someone's browser.
func TestEveryLinkedFileIsServed(t *testing.T) {
	link := regexp.MustCompile(`<(?:link rel="stylesheet" href|script defer src)="\{\{root\}\}(/static/[^"]+)">`)
	files := staticHandler()
	seen := 0
	for _, s := range templateSources(t) {
		for _, m := range link.FindAllStringSubmatch(s, -1) {
			seen++
			if m[1] == "/static/theme.css" {
				continue // Served from the database's setting, and tested with it.
			}
			w := httptest.NewRecorder()
			files.ServeHTTP(w, httptest.NewRequest(http.MethodGet, m[1], nil))
			if w.Code != http.StatusOK {
				t.Errorf("%s: %d", m[1], w.Code)
			}
		}
	}
	if seen < 8 {
		t.Errorf("found %d links, want the stylesheets and scripts on the layout and the stylesheets on the login pages", seen)
	}
}

// The layout and the login pages link the stylesheets that the feed page is told
// about, in the same order, since the theme has to come after Pico.
func TestStylesheetsAreTheOnesPagesLink(t *testing.T) {
	var want strings.Builder
	for _, s := range Stylesheets {
		want.WriteString(`<link rel="stylesheet" href="{{root}}/` + s + `">` + "\n")
	}
	layout, err := templateFiles.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{"layout": string(layout), "pageFrame": pageFrame} {
		if !strings.Contains(src, want.String()) {
			t.Errorf("%s doesn't link %v in that order", name, Stylesheets)
		}
	}
}

// A script is a file of ours, never inline, and nothing uses hx-on, which htmx runs
// with eval. The policy would stop both, and this says so before a browser does.
func TestTemplatesHaveNoInlineScripts(t *testing.T) {
	script := regexp.MustCompile(`<script[^>]*>`)
	ours := regexp.MustCompile(`^<script defer src="\{\{root\}\}/static/[^"]+">$`)
	for _, s := range templateSources(t) {
		for _, tag := range script.FindAllStringIndex(s, -1) {
			opening := s[tag[0]:tag[1]]
			if !ours.MatchString(opening) || !strings.HasPrefix(s[tag[1]:], "</script>") {
				t.Errorf("a script that isn't one of our files: %s", opening)
			}
		}
		if strings.Contains(s, "hx-on") {
			t.Error("a template uses hx-on")
		}
	}
}

func templateSources(t *testing.T) []string {
	t.Helper()
	sources := []string{pageFrame}
	err := fs.WalkDir(templateFiles, "templates", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := templateFiles.ReadFile(path)
		sources = append(sources, string(body))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return sources
}

// A vendored file has its version in its path, so a browser can keep it for good.
// Our own stylesheet changes without its name changing, so it can't.
func TestVendoredFilesAreCachedForGood(t *testing.T) {
	files := staticHandler()
	for path, want := range map[string]string{
		"/static/vendor/pico-2.1.1/pico.min.css": "public, max-age=31536000, immutable",
		"/static/vendor/htmx-4.0.1/htmx.min.js":  "public, max-age=31536000, immutable",
		"/static/style.css":                      "public, max-age=3600",
		"/static/site.js":                        "public, max-age=3600",
	} {
		w := httptest.NewRecorder()
		files.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if got := w.Header().Get("Cache-Control"); w.Code != http.StatusOK || got != want {
			t.Errorf("%s: %d, Cache-Control %q, want %q", path, w.Code, got, want)
		}
		want := map[string]string{".css": "text/css", ".js": "text/javascript"}[filepath.Ext(path)]
		if !strings.HasPrefix(w.Header().Get("Content-Type"), want) {
			t.Errorf("%s: Content-Type %q, want %s", path, w.Header().Get("Content-Type"), want)
		}
	}
}
