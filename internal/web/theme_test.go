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
	return f.themeCSSFor(t, person{}, etag)
}

func (f *fixture) themeCSSFor(t *testing.T, p person, etag string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/static/theme.css", nil)
	if err != nil {
		t.Fatal(err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if p.session != "" {
		req.AddCookie(&http.Cookie{Name: f.session, Value: p.session})
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
		resp.Header.Get("Cache-Control") != "private, no-cache" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/css") {
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

func TestPeoplePickTheirOwnTheme(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	bob := f.person(t, "bob", false)
	themes := theme.MustBuiltin()
	def, _ := themes.Lookup(theme.Default)
	paper, _ := themes.Lookup("paper")
	slate, _ := themes.Lookup("slate")

	account := f.get(t, bob, "/account")
	if !strings.Contains(account.body, `name="theme" value="" checked`) || !strings.Contains(account.body, `value="slate">`) {
		t.Errorf("account page by default: %s", account.body)
	}
	if got := f.post(t, bob, "/account/theme", url.Values{"theme": {"plaid"}}); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, "no theme called plaid") {
		t.Errorf("a theme there isn't: %d %s", got.status, got.body)
	}
	if got := f.post(t, bob, "/account/theme", url.Values{"theme": {"slate"}}); got.status != http.StatusSeeOther {
		t.Fatalf("picking slate: %d %s", got.status, got.body)
	}
	if got := f.get(t, bob, "/account"); !strings.Contains(got.body, `name="theme" value="slate" checked`) {
		t.Errorf("account page after: %s", got.body)
	}
	if _, body := f.themeCSSFor(t, bob, ""); body != string(slate.CSS) {
		t.Errorf("bob's theme: %s", body)
	}
	// Everyone else still gets the site's, and a change to the site's doesn't change bob's.
	if _, body := f.themeCSSFor(t, dave, ""); body != string(def.CSS) {
		t.Errorf("dave's theme: %s", body)
	}
	if err := f.store.SetTheme(t.Context(), "paper"); err != nil {
		t.Fatal(err)
	}
	if _, body := f.themeCSSFor(t, dave, ""); body != string(paper.CSS) {
		t.Errorf("dave's theme after the site's changed: %s", body)
	}
	if _, body := f.themeCSSFor(t, bob, ""); body != string(slate.CSS) {
		t.Errorf("bob's theme after the site's changed: %s", body)
	}
	// A theme of bob's that this server doesn't have gets him the site's.
	if _, err := f.store.SetUserTheme(t.Context(), bob.actor, "gone"); err != nil {
		t.Fatal(err)
	}
	if _, body := f.themeCSSFor(t, bob, ""); body != string(paper.CSS) {
		t.Errorf("bob's unknown theme: %s", body)
	}
	if got := f.post(t, bob, "/account/theme", url.Values{"theme": {""}}); got.status != http.StatusSeeOther {
		t.Fatalf("back to the site's: %d %s", got.status, got.body)
	}
	if _, body := f.themeCSSFor(t, bob, ""); body != string(paper.CSS) {
		t.Errorf("bob following the site's: %s", body)
	}
}
