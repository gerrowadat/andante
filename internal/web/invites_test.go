// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/oidctest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var inviteLink = regexp.MustCompile(`/invite/([A-Za-z0-9_-]+)`)

func TestInvitingFromThePage(t *testing.T) {
	f := newFixture(t)
	dave, bob := f.person(t, "dave", true), f.person(t, "bob", false)

	if got := f.get(t, bob, "/"); strings.Contains(got.body, `href="/invites"`) {
		t.Error("bob is shown invites while only admins can invite")
	}
	if got := f.get(t, bob, "/invites"); got.status != http.StatusOK || strings.Contains(got.body, "Invite someone") {
		t.Errorf("bob's invites page: %d %s", got.status, got.body)
	}
	if got := f.post(t, bob, "/invites", url.Values{"uses": {"1"}}); got.status != http.StatusForbidden {
		t.Errorf("bob inviting: %d", got.status)
	}
	if got := f.post(t, dave, "/users/invite-settings", url.Values{"users": {"1"}, "limit": {"2"}}); got.status != http.StatusSeeOther {
		t.Fatalf("let users invite: %d %s", got.status, got.body)
	}
	if got := f.get(t, dave, "/users"); !strings.Contains(got.body, `name="users" value="1" checked`) || !strings.Contains(got.body, `value="2"`) {
		t.Errorf("the users page doesn't show the settings: %s", got.body)
	}
	if got := f.get(t, bob, "/"); !strings.Contains(got.body, `href="/invites"`) {
		t.Error("bob isn't shown invites")
	}
	if got := f.post(t, bob, "/invites", url.Values{"uses": {"3"}, "days": {"7"}}); got.status != http.StatusConflict {
		t.Errorf("past bob's limit: %d", got.status)
	}
	if got := f.post(t, bob, "/invites", url.Values{"uses": {"1"}, "admin": {"1"}}); got.status != http.StatusForbidden {
		t.Errorf("bob inviting an admin: %d", got.status)
	}
	made := f.post(t, bob, "/invites", url.Values{"uses": {"2"}, "days": {"3"}})
	link := inviteLink.FindStringSubmatch(made.body)
	if made.status != http.StatusOK || link == nil || !strings.Contains(made.body, "Up to 2 people") {
		t.Fatalf("bob's link: %d %s", made.status, made.body)
	}
	if !strings.Contains(made.body, "you have 0 left") {
		t.Errorf("bob isn't told he's out of places: %s", made.body)
	}
	raw := link[1]

	// Whoever uses it picks a name first.
	page := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, "")
	if !strings.Contains(page.body, "Pick a username") || strings.Contains(page.body, `type="password"`) {
		t.Errorf("the invite page: %s", page.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"no good!"}}, home, ""); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a bad name: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"dave"}}, home, ""); got.status != http.StatusConflict {
		t.Errorf("a taken name: %d", got.status)
	}
	picked := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"Dan"}}, home, "")
	if picked.status != http.StatusOK || !strings.Contains(picked.body, `name="name" value="dan"`) || !strings.Contains(picked.body, `type="password"`) {
		t.Fatalf("picking dan: %d %s", picked.status, picked.body)
	}
	in := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"dan"}, "password": {phrase}}, home, "")
	if in.status != http.StatusSeeOther || in.cookie == nil {
		t.Fatalf("dan using it: %d %s", in.status, in.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"dan"}, "password": {phrase}}, away, ""); got.status != http.StatusConflict {
		t.Errorf("dan again: %d", got.status)
	}
	if got := f.get(t, dave, "/users"); !regexp.MustCompile(`<td>dan</td>.*<td>bob</td>`).MatchString(got.body) {
		t.Errorf("the users page doesn't say bob invited dan: %s", got.body)
	}

	listed := f.get(t, dave, "/invites")
	if !strings.Contains(listed.body, "1 of 2") || !strings.Contains(listed.body, "<td>bob</td>") {
		t.Errorf("dave's invites page: %s", listed.body)
	}
	id := regexp.MustCompile(`/invites/(\d+)/withdraw`).FindStringSubmatch(listed.body)
	if id == nil {
		t.Fatal("nothing to withdraw")
	}
	if got := f.post(t, bob, "/invites/"+id[1]+"/withdraw", nil); got.status != http.StatusSeeOther {
		t.Errorf("bob withdrawing it: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("a withdrawn link: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"eve"}, "password": {phrase}}, home, ""); got.status != http.StatusNotFound {
		t.Errorf("using a withdrawn link: %d", got.status)
	}
	want := `
		# HELP andante_invite_uses_total Invites used, by result: ok, or refused when the link was used up, withdrawn or out of date.
		# TYPE andante_invite_uses_total counter
		andante_invite_uses_total{result="ok"} 3
		andante_invite_uses_total{result="refused"} 1
	`
	// dave and bob came in on invites too.
	if err := testutil.GatherAndCompare(f.registry, strings.NewReader(want), "andante_invite_uses_total"); err != nil {
		t.Error(err)
	}
}

func TestAdminsInviteFromTheInvitesPage(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	if got := f.post(t, dave, "/invites", url.Values{"name": {"bob"}, "uses": {"2"}}); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a name for two: %d", got.status)
	}
	if got := f.post(t, dave, "/invites", url.Values{"name": {"dave"}, "uses": {"1"}}); got.status != http.StatusConflict {
		t.Errorf("a taken name: %d", got.status)
	}
	made := f.post(t, dave, "/invites", url.Values{"name": {"carol"}, "uses": {"1"}, "admin": {"1"}})
	link := inviteLink.FindStringSubmatch(made.body)
	if made.status != http.StatusOK || link == nil || !strings.Contains(made.body, "Send this to carol") {
		t.Fatalf("an invite for carol: %d %s", made.status, made.body)
	}
	page := f.do(t, http.MethodGet, "/invite/"+link[1], nil, home, "")
	if !strings.Contains(page.body, "Welcome, carol") || !strings.Contains(page.body, `type="password"`) {
		t.Errorf("carol's invite page: %s", page.body)
	}
	in := f.do(t, http.MethodPost, "/invite/"+link[1], url.Values{"password": {phrase}}, home, "")
	if in.status != http.StatusSeeOther {
		t.Fatalf("carol using it: %d %s", in.status, in.body)
	}
	carol, err := f.store.GetUserByName(t.Context(), "carol")
	if err != nil || carol.Role != store.RoleAdmin || carol.InvitedBy == nil || *carol.InvitedBy != "dave" {
		t.Errorf("carol = %+v, %v", carol, err)
	}
}

func TestPickingANameBeforeSingleSignOn(t *testing.T) {
	f, p := withProvider(t, true)
	f.serve(t, "https://andante.example", false)
	_, raw, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Role: store.RoleUser, MaxUses: 2}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if page := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); strings.Contains(page.body, "/oidc") {
		t.Errorf("single sign-on before a name was picked: %s", page.body)
	}
	// Turned away at the single sign-on link, the form to pick again goes back to the invite.
	if got := f.do(t, http.MethodGet, "/invite/"+raw+"/oidc", nil, home, ""); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, `action="/invite/`+raw+`"`) {
		t.Errorf("no name: %d %s", got.status, got.body)
	}
	picked := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"sam"}}, home, "")
	if !strings.Contains(picked.body, "/invite/"+raw+"/oidc?name=sam") {
		t.Fatalf("after picking a name: %s", picked.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"name": {"sam"}, "password": {phrase}}, home, ""); got.status != http.StatusMethodNotAllowed {
		t.Errorf("a password while passwords are off: %d", got.status)
	}
	who := oidctest.Identity{Subject: "sam-at-idp", Email: "sam@other.org"}
	in := f.sso(t, p, "/invite/"+raw+"/oidc?name=sam", who)
	if in.status != http.StatusSeeOther || in.cookie == nil {
		t.Fatalf("sam: %d %s", in.status, in.body)
	}
	if sam, err := f.store.GetUserByName(t.Context(), "sam"); err != nil || sam.ID != f.whoIs(t, in.cookie.Value).UserID {
		t.Errorf("sam = %+v, %v", sam, err)
	}
}

func TestTheInvitesPageGoesPastOnePage(t *testing.T) {
	f := newFixture(t)
	f.person(t, "dave", true)
	bob := f.person(t, "bob", false)
	who, limit := store.InvitesByUsers, int32(150)
	if err := f.store.SetInviteSettings(t.Context(), &who, &limit); err != nil {
		t.Fatal(err)
	}
	var last int64
	for range 101 {
		i, _, err := f.store.CreateInvite(t.Context(), bob.actor, store.InviteRequest{Role: store.RoleUser, MaxUses: 1}, f.clock())
		if err != nil {
			t.Fatal(err)
		}
		last = i.ID
	}
	first := f.get(t, bob, "/invites")
	more := regexp.MustCompile(`href="/invites\?after=(\d+)"`).FindStringSubmatch(first.body)
	if more == nil || strings.Count(first.body, "/withdraw") != 100 || !strings.Contains(first.body, "you have 49 left") {
		t.Fatalf("the first page: %s", first.body)
	}
	// Places are counted across every invite, whichever page is showing.
	second := f.get(t, bob, "/invites?after="+more[1])
	if strings.Count(second.body, "/withdraw") != 1 || !strings.Contains(second.body, fmt.Sprintf("/invites/%d/withdraw", last)) ||
		strings.Contains(second.body, "after=") || !strings.Contains(second.body, "you have 49 left") {
		t.Errorf("the second page: %s", second.body)
	}
}
