// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

const pendingName = "__Host-andante_pending"

var (
	keyShown      = regexp.MustCompile(`Key: <code>([A-Z2-7 ]+)</code>`)
	recoveryShown = regexp.MustCompile(`<li><code>([a-z2-7-]+)</code></li>`)
)

func (f *fixture) code(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// enrol sets two-factor up for someone from the pages, and returns the secret and
// their recovery codes.
func (f *fixture) enrol(t *testing.T, p person) (string, []string) {
	t.Helper()
	page := f.get(t, p, "/account/two-factor")
	match := keyShown.FindStringSubmatch(page.body)
	if page.status != http.StatusOK || match == nil {
		t.Fatalf("set-up page: %d %s", page.status, page.body)
	}
	secret := strings.ReplaceAll(match[1], " ", "")
	if qr := f.get(t, p, "/account/two-factor/qr"); qr.status != http.StatusOK || qr.header.Get("Content-Type") != "image/png" {
		t.Errorf("QR code: %d %s", qr.status, qr.header.Get("Content-Type"))
	}
	if got := f.post(t, p, "/account/two-factor", url.Values{"code": {"000000"}}); got.status != http.StatusUnprocessableEntity || !strings.Contains(got.body, match[1]) {
		t.Errorf("a wrong code: %d, and the same key again: %v", got.status, strings.Contains(got.body, match[1]))
	}
	done := f.post(t, p, "/account/two-factor", url.Values{"code": {f.code(t, secret)}})
	var codes []string
	for _, m := range recoveryShown.FindAllStringSubmatch(done.body, -1) {
		codes = append(codes, m[1])
	}
	if done.status != http.StatusOK || len(codes) != 10 {
		t.Fatalf("turning it on: %d, %d codes", done.status, len(codes))
	}
	if qr := f.get(t, p, "/account/two-factor/qr"); qr.status != http.StatusNotFound {
		t.Errorf("the QR code once it's on: %d", qr.status)
	}
	return secret, codes
}

// secondStep logs in with a password and gives a code, and returns the answer to the
// code.
func (f *fixture) secondStep(t *testing.T, name, code string) answer {
	t.Helper()
	first := f.login(t, name, phrase, home)
	pending := first.cookies[pendingName]
	if first.status != http.StatusSeeOther || first.header.Get("Location") != "/login/code" || first.cookie != nil || pending == nil {
		t.Fatalf("password: %d %q, session %v", first.status, first.header.Get("Location"), first.cookie)
	}
	if got := f.do(t, http.MethodGet, "/login/code", nil, home, "", "Cookie", pendingName+"="+pending.Value); got.status != http.StatusOK {
		t.Errorf("code page: %d", got.status)
	}
	return f.do(t, http.MethodPost, "/login/code", url.Values{"code": {code}}, home, "", "Cookie", pendingName+"="+pending.Value)
}

func TestTwoFactor(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", false)
	if page := f.get(t, dave, "/account"); !strings.Contains(page.body, "/account/two-factor") {
		t.Errorf("no way to set it up: %s", page.body)
	}
	secret, recovery := f.enrol(t, dave)

	// The code that turned it on can't log in, nor can any other code for that step.
	if got := f.secondStep(t, "dave", f.code(t, secret)); got.status != http.StatusUnauthorized || got.cookie != nil {
		t.Errorf("the set-up code again: %d", got.status)
	}
	f.advance(30 * time.Second)
	code := f.code(t, secret)
	in := f.secondStep(t, "dave", code)
	if in.status != http.StatusSeeOther || in.cookie == nil || in.cookies[pendingName] == nil || in.cookies[pendingName].MaxAge >= 0 {
		t.Fatalf("a fresh code: %d", in.status)
	}
	if got := f.secondStep(t, "dave", code); got.status != http.StatusUnauthorized || got.cookie != nil {
		t.Errorf("the same code twice: %d", got.status)
	}

	if got := f.secondStep(t, "dave", strings.ToUpper(recovery[0])); got.status != http.StatusSeeOther || got.cookie == nil {
		t.Errorf("a recovery code: %d", got.status)
	}
	if got := f.secondStep(t, "dave", recovery[0]); got.status != http.StatusUnauthorized {
		t.Errorf("the same recovery code twice: %d", got.status)
	}

	// Without the first step, or with it tampered with, there's nothing to finish.
	if got := f.do(t, http.MethodPost, "/login/code", url.Values{"code": {recovery[1]}}, home, ""); got.status != http.StatusSeeOther || got.header.Get("Location") != "/login" {
		t.Errorf("no password first: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/login/code", url.Values{"code": {recovery[1]}}, home, "", "Cookie", pendingName+"=AAAA"); got.status != http.StatusSeeOther {
		t.Errorf("a made-up first step: %d", got.status)
	}
	first := f.login(t, "dave", phrase, home)
	f.advance(11 * time.Minute)
	if got := f.do(t, http.MethodPost, "/login/code", url.Values{"code": {recovery[1]}}, home, "", "Cookie", pendingName+"="+first.cookies[pendingName].Value); got.status != http.StatusSeeOther || got.cookie != nil {
		t.Errorf("a stale first step: %d", got.status)
	}

	// Guessing codes is throttled like guessing passwords.
	pending := f.login(t, "dave", phrase, away).cookies[pendingName].Value
	var last int
	for range 5 {
		last = f.do(t, http.MethodPost, "/login/code", url.Values{"code": {"000000"}}, away, "", "Cookie", pendingName+"="+pending).status
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("guessing: %d", last)
	}

	if page := f.get(t, dave, "/account"); !strings.Contains(page.body, "9 recovery codes left") {
		t.Errorf("account page: %s", page.body)
	}
	if got := f.post(t, dave, "/account/two-factor/off", url.Values{"code": {"000000"}}); got.status != http.StatusUnauthorized {
		t.Errorf("turning it off with a wrong code: %d", got.status)
	}
	f.advance(time.Minute)
	if got := f.post(t, dave, "/account/two-factor/off", url.Values{"code": {f.code(t, secret)}}); got.status != http.StatusSeeOther {
		t.Errorf("turning it off: %d", got.status)
	}
	if got := f.login(t, "dave", phrase, home); got.status != http.StatusSeeOther || got.cookie == nil {
		t.Errorf("a password alone once it's off: %d", got.status)
	}
}

func TestAdminsCanRequireTwoFactor(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	bob := f.person(t, "bob", false)
	if got := f.post(t, dave, "/users/two-factor", url.Values{"require": {"1"}}); got.status != http.StatusSeeOther {
		t.Fatalf("require it: %d", got.status)
	}
	for _, p := range []person{dave, bob} {
		if got := f.get(t, p, "/sources"); got.status != http.StatusSeeOther || got.header.Get("Location") != "/account/two-factor" {
			t.Errorf("a page without it: %d %q", got.status, got.header.Get("Location"))
		}
	}
	if got := f.post(t, bob, "/account/tokens", url.Values{"label": {"laptop"}}); got.status != http.StatusForbidden {
		t.Errorf("a form without it: %d", got.status)
	}
	f.enrol(t, bob)
	if got := f.get(t, bob, "/sources"); got.status != http.StatusOK {
		t.Errorf("a page with it: %d", got.status)
	}

	f.enrol(t, dave)
	users := f.get(t, dave, "/users")
	if !strings.Contains(users.body, "checked") || !strings.Contains(users.body, "Turn off two-factor") {
		t.Errorf("users page: %s", users.body)
	}
	if got := f.post(t, dave, "/users/"+strconv.FormatInt(bob.actor.UserID, 10)+"/two-factor/off", nil); got.status != http.StatusSeeOther {
		t.Errorf("turning off bob's: %d", got.status)
	}
	if got := f.get(t, bob, "/sources"); got.status != http.StatusSeeOther {
		t.Errorf("bob after his was turned off: %d", got.status)
	}
	if got := f.post(t, dave, "/users/two-factor", nil); got.status != http.StatusSeeOther {
		t.Fatalf("stop requiring it: %d", got.status)
	}
	if got := f.get(t, bob, "/sources"); got.status != http.StatusOK {
		t.Errorf("bob once it's optional: %d", got.status)
	}
}
