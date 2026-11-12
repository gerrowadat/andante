// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/oidctest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/web"
)

const oidcName = "__Host-andante_oidc"

func withProvider(t *testing.T, passwordLogin bool) (*fixture, *oidctest.Provider) {
	t.Helper()
	f := newFixture(t)
	p := oidctest.New(t)
	f.oidc = &web.OIDCOptions{
		Issuer: p.Issuer(), ClientID: p.ClientID, ClientSecret: p.ClientSecret,
		AllowedDomains: []string{"example.com"}, AllowedGroups: []string{"readers"}, HTTPClient: p.Client(),
	}
	f.serve(t, "https://andante.example", passwordLogin)
	return f, p
}

// sso goes through a login at the provider, starting at start, and returns
// andante's answer to the provider sending the browser back.
func (f *fixture) sso(t *testing.T, p *oidctest.Provider, start string, who oidctest.Identity) answer {
	t.Helper()
	p.LogIn(who)
	begin := f.do(t, http.MethodGet, start, nil, home, "")
	flow := begin.cookies[oidcName]
	to, err := url.Parse(begin.header.Get("Location"))
	if begin.status != http.StatusSeeOther || flow == nil || err != nil || !strings.HasPrefix(to.String(), p.Issuer()) {
		t.Fatalf("start: %d %q", begin.status, begin.header.Get("Location"))
	}
	q := to.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("redirect_uri") != "https://andante.example/login/oidc/callback" {
		t.Errorf("authorization request: %s", to)
	}
	client := p.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, to.String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	back, err := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusFound || err != nil {
		t.Fatalf("provider: %d %v", resp.StatusCode, err)
	}
	return f.do(t, http.MethodGet, back.RequestURI(), nil, home, "", "Cookie", oidcName+"="+flow.Value)
}

func (f *fixture) whoIs(t *testing.T, session string) store.Session {
	t.Helper()
	got, err := f.store.LookupSession(t.Context(), session, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAnEmailCantTakeOverAnAccount(t *testing.T) {
	f, p := withProvider(t, true)
	dave := f.person(t, "dave", true)
	who := oidctest.Identity{Subject: "1", Email: "dave@example.com", EmailVerified: true, PreferredUsername: "dave"}
	in := f.sso(t, p, "/login/oidc", who)
	if in.status != http.StatusSeeOther || in.cookie == nil {
		t.Fatalf("login: %d %s", in.status, in.body)
	}
	got := f.whoIs(t, in.cookie.Value)
	if got.UserID == dave.actor.UserID || got.Name != "dave-2" || got.Role != store.RoleUser || got.Method != store.MethodOIDC {
		t.Errorf("logged in as %+v", got)
	}
	// The second time is the same new user.
	if again := f.sso(t, p, "/login/oidc", who); again.cookie == nil || f.whoIs(t, again.cookie.Value).UserID != got.UserID {
		t.Errorf("again: %d", again.status)
	}
}

func TestWhoCanJoinWithAProvider(t *testing.T) {
	f, p := withProvider(t, true)
	if page := f.do(t, http.MethodGet, "/login", nil, home, ""); !strings.Contains(page.body, `href="/login/oidc"`) || !strings.Contains(page.body, `type="password"`) {
		t.Errorf("login page: %s", page.body)
	}
	for _, c := range []struct {
		name string
		who  oidctest.Identity
		want int
	}{
		{"an allowed domain", oidctest.Identity{Subject: "a", Email: "Alex@Example.com", EmailVerified: true}, http.StatusSeeOther},
		{"an unverified email", oidctest.Identity{Subject: "b", Email: "bob@example.com"}, http.StatusForbidden},
		{"a lookalike domain", oidctest.Identity{Subject: "c", Email: "c@notexample.com", EmailVerified: true}, http.StatusForbidden},
		{"an allowed group", oidctest.Identity{Subject: "d", Email: "d@other.org", Groups: []string{"staff", "readers"}}, http.StatusSeeOther},
		{"neither", oidctest.Identity{Subject: "e", Email: "e@other.org", EmailVerified: true, Groups: []string{"staff"}}, http.StatusForbidden},
	} {
		if got := f.sso(t, p, "/login/oidc", c.who); got.status != c.want || (got.cookie != nil) != (c.want == http.StatusSeeOther) {
			t.Errorf("%s: %d", c.name, got.status)
		}
	}
	users, err := f.store.ListUsers(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range users {
		names = append(names, u.Name)
	}
	if strings.Join(names, ",") != "alex,d" {
		t.Errorf("users = %v", names)
	}
}

func TestAnInviteLinksAProvider(t *testing.T) {
	f, p := withProvider(t, true)
	dave := f.person(t, "dave", false)
	f.serve(t, "https://andante.example", false)
	_, raw, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Name: "dave", Reset: true}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if page := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); !strings.Contains(page.body, "/invite/"+raw+"/oidc") || strings.Contains(page.body, `type="password"`) {
		t.Errorf("invite page: %s", page.body)
	}
	// Not in an allowed domain, but the invite is enough.
	who := oidctest.Identity{Subject: "dave-at-idp", Email: "dave@other.org"}
	in := f.sso(t, p, "/invite/"+raw+"/oidc", who)
	if in.status != http.StatusSeeOther || in.cookie == nil || f.whoIs(t, in.cookie.Value).UserID != dave.actor.UserID {
		t.Fatalf("redeem: %d %s", in.status, in.body)
	}
	if got := f.get(t, dave, "/"); got.status != http.StatusSeeOther {
		t.Errorf("the old session lived on: %d", got.status)
	}
	if again := f.sso(t, p, "/login/oidc", who); again.cookie == nil || f.whoIs(t, again.cookie.Value).UserID != dave.actor.UserID {
		t.Errorf("login: %d", again.status)
	}
	if got := f.do(t, http.MethodGet, "/invite/"+raw+"/oidc", nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("a used invite: %d", got.status)
	}

	_, other, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Name: "alex", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.sso(t, p, "/invite/"+other+"/oidc", who); got.status != http.StatusConflict || got.cookie != nil {
		t.Errorf("someone else's login on an invite: %d", got.status)
	}

	yes := true
	if _, err := f.store.UpdateUser(t.Context(), dave.actor.UserID, store.UserChange{Disabled: &yes}, f.clock()); err != nil {
		t.Fatal(err)
	}
	if got := f.sso(t, p, "/login/oidc", who); got.status != http.StatusForbidden || got.cookie != nil {
		t.Errorf("disabled: %d", got.status)
	}
}

func TestAForgedReturnFromTheProvider(t *testing.T) {
	f, p := withProvider(t, true)
	who := oidctest.Identity{Subject: "a", Email: "a@example.com", EmailVerified: true}
	p.LogIn(who)

	begin := f.do(t, http.MethodGet, "/login/oidc", nil, home, "")
	flow := begin.cookies[oidcName].Value
	cookie := oidcName + "=" + flow
	if got := f.do(t, http.MethodGet, "/login/oidc/callback?code=x&state=y", nil, home, ""); got.status != http.StatusBadRequest {
		t.Errorf("no flow cookie: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/login/oidc/callback?code=x&state=y", nil, home, "", "Cookie", cookie); got.status != http.StatusBadRequest {
		t.Errorf("the wrong state: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/login/oidc/callback?error=access_denied", nil, home, "", "Cookie", cookie); got.status != http.StatusBadRequest {
		t.Errorf("the provider said no: %d", got.status)
	}

	p.SetNonce("not-the-one")
	if got := f.sso(t, p, "/login/oidc", who); got.status != http.StatusBadRequest || got.cookie != nil {
		t.Errorf("the wrong nonce: %d", got.status)
	}
}
