// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package rootpath serves andante-server from a path under its host, such as
// https://example.com/feeds, taken from the path of --base-url.
package rootpath

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// segments is what a root path may be made of. Anything else would need escaping in
// a URL, a cookie's path or a template, and nobody needs it.
var segments = regexp.MustCompile(`^(/[A-Za-z0-9._~-]+)+$`)

// FromBaseURL is the root path in a base URL: its path without the trailing slash,
// or "" when it's served from the top of the host.
func FromBaseURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q has a query or fragment", raw)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		return "", nil
	}
	if !segments.MatchString(path) || strings.Contains(path, "/./") || strings.Contains(path, "/../") ||
		strings.HasSuffix(path, "/.") || strings.HasSuffix(path, "/..") {
		return "", fmt.Errorf("the path %q can only have letters, digits and . _ ~ - between its slashes", path)
	}
	return path, nil
}

// Mount serves h under root. A request for root itself is sent on to root + "/",
// and anything outside root is a 404. h sees paths with root taken off, and a
// Location it sets that starts with a single slash gets root put back, so that
// redirects written for the top of the host still land in the right place.
func Mount(root string, h http.Handler) http.Handler {
	if root == "" {
		return h
	}
	strip := http.StripPrefix(root, h)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == root:
			http.Redirect(w, r, root+"/", http.StatusMovedPermanently)
		case strings.HasPrefix(r.URL.Path, root+"/"):
			strip.ServeHTTP(&located{ResponseWriter: w, root: root}, r)
		default:
			http.NotFound(w, r)
		}
	})
}

// Cookie is the path for a cookie that every page under root should get.
func Cookie(root string) string {
	if root == "" {
		return "/"
	}
	return root
}

type located struct {
	http.ResponseWriter
	root  string
	fixed bool
}

func (l *located) fix() {
	if l.fixed {
		return
	}
	l.fixed = true
	h := l.Header()
	if loc := h.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		h.Set("Location", l.root+loc)
	}
}

func (l *located) WriteHeader(code int) {
	l.fix()
	l.ResponseWriter.WriteHeader(code)
}

func (l *located) Write(b []byte) (int, error) {
	l.fix()
	return l.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the connection.
func (l *located) Unwrap() http.ResponseWriter { return l.ResponseWriter }
