// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/oidctest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

var bootstrapToken = strings.Repeat("b", 32)

func (f *fixture) withSetup(t *testing.T, passwordLogin bool) {
	t.Helper()
	f.setup = bootstrap.New(bootstrap.Options{
		Store: f.store, Token: bootstrapToken, BaseURL: "https://andante.example",
		Registry: prometheus.NewRegistry(), Logger: slog.New(slog.DiscardHandler), Now: f.clock,
	})
	f.serve(t, "https://andante.example", passwordLogin)
}

func setupForm(token, name string) url.Values {
	return url.Values{"token": {token}, "name": {name}}
}

func TestSettingUpTheFirstAdmin(t *testing.T) {
	f := newFixture(t)
	if got := f.do(t, http.MethodGet, "/setup", nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("setup with no bootstrap: %d", got.status)
	}
	f.withSetup(t, true)
	if got := f.do(t, http.MethodGet, "/setup", nil, home, ""); got.status != http.StatusOK || !strings.Contains(got.body, `name="token"`) {
		t.Fatalf("setup page: %d %s", got.status, got.body)
	}
	if got := f.do(t, http.MethodPost, "/setup", setupForm("guess", "dave"), home, ""); got.status != http.StatusUnauthorized || !strings.Contains(got.body, "isn&#39;t the bootstrap token") {
		t.Errorf("a wrong token: %d %s", got.status, got.body)
	}

	// Someone who got in some other way keeps their account.
	f.user(t, "alex")
	if got := f.do(t, http.MethodPost, "/setup", setupForm(bootstrapToken, "alex"), home, ""); got.status != http.StatusConflict {
		t.Errorf("taking over alex: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/setup", setupForm(bootstrapToken, "Not a name"), home, ""); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a bad name: %d", got.status)
	}

	got := f.do(t, http.MethodPost, "/setup", setupForm(bootstrapToken, " Dave"), home, "")
	invite := got.header.Get("Location")
	if got.status != http.StatusSeeOther || !strings.HasPrefix(invite, "/invite/") {
		t.Fatalf("setup: %d %q", got.status, invite)
	}
	in := f.do(t, http.MethodPost, invite, url.Values{"password": {phrase}}, home, "")
	if in.status != http.StatusSeeOther || in.cookie == nil {
		t.Fatalf("invite: %d %s", in.status, in.body)
	}
	if who := f.whoIs(t, in.cookie.Value); who.Name != "dave" || who.Role != store.RoleAdmin {
		t.Errorf("made %+v", who)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		if got := f.do(t, method, "/setup", setupForm(bootstrapToken, "sam"), home, ""); got.status != http.StatusNotFound {
			t.Errorf("%s with an admin: %d", method, got.status)
		}
	}
}

func TestSetupIsThrottled(t *testing.T) {
	f := newFixture(t)
	f.withSetup(t, true)
	var got answer
	for range 30 {
		if got = f.do(t, http.MethodPost, "/setup", setupForm("guess", "dave"), away, ""); got.status == http.StatusTooManyRequests {
			break
		}
	}
	if got.status != http.StatusTooManyRequests || got.header.Get("Retry-After") == "" {
		t.Errorf("after guessing: %d", got.status)
	}
}

func TestSettingUpWithTheProviderAlone(t *testing.T) {
	f, p := withProvider(t, false)
	f.withSetup(t, false)
	got := f.do(t, http.MethodPost, "/setup", setupForm(bootstrapToken, "dave"), home, "")
	invite := got.header.Get("Location")
	if got.status != http.StatusSeeOther {
		t.Fatalf("setup: %d %s", got.status, got.body)
	}
	if page := f.do(t, http.MethodGet, invite, nil, home, ""); !strings.Contains(page.body, invite+"/oidc") || strings.Contains(page.body, `type="password"`) {
		t.Errorf("invite page: %s", page.body)
	}
	// Not in an allowed domain, which doesn't come into it.
	in := f.sso(t, p, invite+"/oidc", oidctest.Identity{Subject: "dave-at-google", Email: "dave@gmail.com"})
	if in.status != http.StatusSeeOther || in.cookie == nil {
		t.Fatalf("redeem: %d %s", in.status, in.body)
	}
	if who := f.whoIs(t, in.cookie.Value); who.Name != "dave" || who.Role != store.RoleAdmin {
		t.Errorf("made %+v", who)
	}
}
