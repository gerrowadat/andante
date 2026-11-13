// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/store"
)

func TestAdminsSetThePasswordPolicy(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	bob := f.person(t, "bob", false)
	strict := url.Values{"min_length": {"20"}, "non_letters": {"1"}, "classes": {"3"}}

	if got := f.get(t, dave, "/users"); !strings.Contains(got.body, `name="min_length" value="8"`) || !strings.Contains(got.body, `name="non_letters" value="1"`) || !strings.Contains(got.body, `name="classes" value="0"`) {
		t.Errorf("users page by default: %s", got.body)
	}
	if got := f.post(t, bob, "/users/password-policy", strict); got.status != http.StatusForbidden {
		t.Errorf("bob setting it: %d", got.status)
	}
	for _, bad := range []url.Values{
		{"min_length": {"6"}, "non_letters": {"1"}, "classes": {"0"}},
		{"min_length": {"15"}, "non_letters": {"1"}, "classes": {"5"}},
		{"min_length": {"8"}, "non_letters": {"9"}, "classes": {"0"}},
		{"min_length": {"lots"}, "non_letters": {"1"}, "classes": {"0"}},
		{"min_length": {"8"}, "classes": {"0"}},
	} {
		if got := f.post(t, dave, "/users/password-policy", bad); got.status != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", bad, got.status)
		}
	}
	if got, err := f.store.GetPasswordPolicy(t.Context()); err != nil || got != password.DefaultPolicy {
		t.Errorf("after refusals = %+v, %v", got, err)
	}
	if got := f.post(t, dave, "/users/password-policy", strict); got.status != http.StatusSeeOther {
		t.Fatalf("setting it: %d", got.status)
	}
	if got := f.get(t, dave, "/users"); !strings.Contains(got.body, `name="min_length" value="20"`) || !strings.Contains(got.body, `name="classes" value="3"`) {
		t.Errorf("users page after: %s", got.body)
	}

	// The account page asks for it, and holds to it.
	account := f.get(t, bob, "/account")
	if !strings.Contains(account.body, `minlength="20"`) || !strings.Contains(account.body, "At least 20 characters, and at least one that isn&#39;t a letter, with at least 3 of") {
		t.Errorf("account page: %s", account.body)
	}
	if got := f.post(t, bob, "/account/password", url.Values{"current": {phrase}, "password": {"another long phrase"}}); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, "at least 3 of") {
		t.Errorf("a password with two kinds: %d %s", got.status, got.body)
	}
	if got := f.post(t, bob, "/account/password", url.Values{"current": {phrase}, "password": {"Another long phrase 2"}}); got.status != http.StatusOK {
		t.Errorf("a password with four kinds: %d %s", got.status, got.body)
	}

	// So does an invite.
	_, raw, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Name: "alex", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); !strings.Contains(got.body, `minlength="20"`) || !strings.Contains(got.body, "At least 20 characters") {
		t.Errorf("invite page: %s", got.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {"a long enough phrase"}}, home, ""); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, "at least 3 of") {
		t.Errorf("a password with two kinds: %d %s", got.status, got.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {"A long enough phrase"}}, home, ""); got.status != http.StatusSeeOther {
		t.Errorf("a password with three kinds: %d %s", got.status, got.body)
	}
}

func TestTheDefaultPolicyWantsANonLetter(t *testing.T) {
	f := newFixture(t)
	bob := f.person(t, "bob", false)
	if got := f.post(t, bob, "/account/password", url.Values{"current": {phrase}, "password": {"onlyletters"}}); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, "a digit, a symbol or a space") {
		t.Errorf("letters only: %d %s", got.status, got.body)
	}
	if got := f.post(t, bob, "/account/password", url.Values{"current": {phrase}, "password": {"letters9"}}); got.status != http.StatusOK {
		t.Errorf("eight with a digit: %d %s", got.status, got.body)
	}
}
