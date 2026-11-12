// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package rootpath_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gerrowadat/andante/internal/rootpath"
)

func TestFromBaseURL(t *testing.T) {
	for raw, want := range map[string]string{
		"https://feeds.example.com":        "",
		"https://feeds.example.com/":       "",
		"https://example.com/feedslow":     "/feedslow",
		"https://example.com/feedslow/":    "/feedslow",
		"https://example.com/a/b-c_d.e~f/": "/a/b-c_d.e~f",
		"http://127.0.0.1:8080/andante":    "/andante",
	} {
		got, err := rootpath.FromBaseURL(raw)
		if err != nil || got != want {
			t.Errorf("FromBaseURL(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"https://example.com/feeds?x=1",
		"https://example.com/feeds#top",
		"https://example.com/fe%20eds",
		"https://example.com/{feeds}",
		"https://example.com/a/../b",
		"https://example.com/a/..",
		"https://example.com//feeds",
	} {
		if got, err := rootpath.FromBaseURL(raw); err == nil {
			t.Errorf("FromBaseURL(%q) = %q, want an error", raw, got)
		}
	}
}

func TestMount(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("home")) })
	inner.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.URL.Path)) })
	inner.HandleFunc("POST /go", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/reads/3", http.StatusSeeOther)
	})
	inner.HandleFunc("POST /away", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.example/x", http.StatusSeeOther)
	})
	inner.HandleFunc("POST /protocol-relative", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "//elsewhere.example/x")
		w.WriteHeader(http.StatusSeeOther)
	})
	inner.HandleFunc("GET /dir/", func(w http.ResponseWriter, r *http.Request) {})
	h := rootpath.Mount("/feedslow", inner)

	for _, c := range []struct {
		method, path string
		status       int
		location     string
		body         string
	}{
		{"GET", "/feedslow/", http.StatusOK, "", "home"},
		{"GET", "/feedslow/healthz", http.StatusOK, "", "/healthz"},
		{"GET", "/feedslow", http.StatusMovedPermanently, "/feedslow/", ""},
		{"GET", "/feedslow?x=1", http.StatusMovedPermanently, "/feedslow/", ""},
		{"POST", "/feedslow/go", http.StatusSeeOther, "/feedslow/reads/3", ""},
		{"POST", "/feedslow/away", http.StatusSeeOther, "https://elsewhere.example/x", ""},
		{"POST", "/feedslow/protocol-relative", http.StatusSeeOther, "//elsewhere.example/x", ""},
		{"GET", "/feedslow/dir", http.StatusTemporaryRedirect, "/feedslow/dir/", ""},
		{"GET", "/healthz", http.StatusNotFound, "", ""},
		{"GET", "/", http.StatusNotFound, "", ""},
		{"GET", "/feedslower/healthz", http.StatusNotFound, "", ""},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		if w.Code != c.status || w.Header().Get("Location") != c.location || (c.body != "" && w.Body.String() != c.body) {
			t.Errorf("%s %s = %d, Location %q, body %q; want %d, %q, %q",
				c.method, c.path, w.Code, w.Header().Get("Location"), w.Body.String(), c.status, c.location, c.body)
		}
	}
}

func TestMountWithoutARootChangesNothing(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
	if h := rootpath.Mount("", inner); h == nil {
		t.Fatal("no handler")
	}
}

func TestCookie(t *testing.T) {
	if got := rootpath.Cookie(""); got != "/" {
		t.Errorf("Cookie(\"\") = %q", got)
	}
	if got := rootpath.Cookie("/feedslow"); got != "/feedslow" {
		t.Errorf("Cookie(/feedslow) = %q", got)
	}
}
