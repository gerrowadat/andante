// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/theme"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func (f *fixture) themeCSS(t *testing.T, etag string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/static/theme.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(body)
}

func TestAdminsChooseTheTheme(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	bob := f.person(t, "bob", false)
	themes := theme.MustBuiltin()
	def, _ := themes.Lookup(theme.Default)
	paper, _ := themes.Lookup("paper")

	// Anyone can fetch it, logged in or not, since the login page and the feed page
	// use it too.
	resp, body := f.themeCSS(t, "")
	if resp.StatusCode != http.StatusOK || body != string(def.CSS) || resp.Header.Get("ETag") != def.ETag ||
		resp.Header.Get("Cache-Control") != "no-cache" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("default theme: %d %v %s", resp.StatusCode, resp.Header, body)
	}
	if resp, _ := f.themeCSS(t, def.ETag); resp.StatusCode != http.StatusNotModified {
		t.Errorf("asking again: %d", resp.StatusCode)
	}

	users := f.get(t, dave, "/users")
	if !strings.Contains(users.body, `name="theme" value="default" checked`) || !strings.Contains(users.body, `value="paper">`) {
		t.Errorf("users page by default: %s", users.body)
	}
	if got := f.post(t, bob, "/users/theme", url.Values{"theme": {"paper"}}); got.status != http.StatusForbidden {
		t.Errorf("bob choosing it: %d", got.status)
	}
	if got := f.post(t, dave, "/users/theme", url.Values{"theme": {"plaid"}}); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, "no theme called plaid") {
		t.Errorf("a theme there isn't: %d %s", got.status, got.body)
	}
	if got := f.post(t, dave, "/users/theme", url.Values{"theme": {"paper"}}); got.status != http.StatusSeeOther {
		t.Fatalf("choosing paper: %d %s", got.status, got.body)
	}
	if got := f.get(t, dave, "/users"); !strings.Contains(got.body, `name="theme" value="paper" checked`) {
		t.Errorf("users page after: %s", got.body)
	}
	// The browser's copy of the default is out of date now.
	resp, body = f.themeCSS(t, def.ETag)
	if resp.StatusCode != http.StatusOK || body != string(paper.CSS) || resp.Header.Get("ETag") != paper.ETag {
		t.Errorf("after choosing paper: %d %s", resp.StatusCode, body)
	}

	// A theme this server doesn't have, say after a downgrade, gets the default.
	if err := f.store.SetTheme(t.Context(), "gone"); err != nil {
		t.Fatal(err)
	}
	if _, body := f.themeCSS(t, ""); body != string(def.CSS) {
		t.Errorf("an unknown theme: %s", body)
	}
	want := `
# HELP andante_web_theme_fallbacks_total Times the website served the default theme in place of the chosen one, by reason: unreadable or unknown.
# TYPE andante_web_theme_fallbacks_total counter
andante_web_theme_fallbacks_total{reason="unknown"} 1
`
	if err := testutil.GatherAndCompare(f.registry, strings.NewReader(want), "andante_web_theme_fallbacks_total"); err != nil {
		t.Error(err)
	}

	// Every page links to it.
	for _, path := range []string{"/", "/login"} {
		who := dave
		if path == "/login" {
			who = person{}
		}
		got := f.get(t, who, path).body
		if !strings.Contains(got, `<link rel="stylesheet" href="/static/theme.css">`) {
			t.Errorf("%s doesn't link the theme: %s", path, got)
		}
	}
}
