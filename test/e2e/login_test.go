// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"

	"github.com/gerrowadat/andante/internal/store/storetest"
)

const sessionCookie = "__Host-andante_session"

func post(t *testing.T, target string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func sessionOf(t *testing.T, resp *http.Response) string {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatalf("no session cookie: %d %v", resp.StatusCode, resp.Header)
	return ""
}

func page(t *testing.T, address, session string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The cookie is Secure, and this is plain HTTP, so a cookie jar wouldn't send it.
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestLoginThroughTheRealServer(t *testing.T) {
	bin := buildServer(t)
	dsn := storetest.DSN(t)
	p := startServer(t, bin, dsn)
	p.waitReady(t)
	p.addUser(t, "dave")

	const words = "a long enough phrase"
	out, err := exec.Command(bin, "user", "link", "dave", "--database-url="+dsn, "--base-url=https://andante.example").Output()
	if err != nil {
		t.Fatalf("user link: %v", err)
	}
	path := strings.TrimPrefix(strings.TrimSpace(string(out)), "https://andante.example")
	set := post(t, "http://"+p.public+path, url.Values{"password": {words}})
	if set.StatusCode != http.StatusSeeOther {
		t.Fatalf("setting a password: %d", set.StatusCode)
	}
	first := sessionOf(t, set)
	if code, body := page(t, p.public, first); code != http.StatusOK || !strings.Contains(body, "dave") {
		t.Fatalf("home: %d %s", code, body)
	}

	stop(t, p)
	p = startServer(t, bin, dsn)
	p.waitReady(t)
	if code, _ := page(t, p.public, first); code != http.StatusOK {
		t.Errorf("a session after a restart: %d", code)
	}

	if resp := post(t, "http://"+p.public+"/login", url.Values{"name": {"dave"}, "password": {"the wrong phrase"}}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the wrong password: %d", resp.StatusCode)
	}
	in := post(t, "http://"+p.public+"/login", url.Values{"name": {"dave"}, "password": {words}})
	if in.StatusCode != http.StatusSeeOther {
		t.Fatalf("logging in: %d", in.StatusCode)
	}
	if strings.Contains(p.output(), sessionOf(t, in)) || strings.Contains(p.output(), words) {
		t.Error("a session or password reached the logs")
	}
	if strings.Contains(p.output(), strings.TrimPrefix(path, "/invite/")) {
		t.Error("an invite reached the logs")
	}
}
