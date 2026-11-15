// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"bytes"
	"net/http"
	"time"

	"github.com/gerrowadat/andante/internal/theme"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Stylesheets are the stylesheets every page links to, in order, relative to the root
// path. The feed page, which isn't one of the website's, links to them as well.
var Stylesheets = []string{"static/vendor/pico-2.1.1/pico.min.css", "static/theme.css", "static/style.css"}

// themeCSS serves the site's theme. It's one URL whichever theme is chosen, so that
// the pages don't need to know, and the browser asks again each time with the ETag,
// so that a change shows on every server on the next page.
func (s *site) themeCSS(w http.ResponseWriter, r *http.Request) {
	themes := theme.MustBuiltin()
	t, _ := themes.Lookup(theme.Default)
	name, err := s.themeName(r, themes)
	switch {
	case err != nil:
		// Pico's own look is better than no stylesheet.
		s.opts.Logger.Warn("reading the theme; using the default", "error", err)
		s.themeFallbacks.WithLabelValues("unreadable").Inc()
	default:
		if chosen, ok := themes.Lookup(name); ok {
			t = chosen
		} else {
			// The setting names a theme this server doesn't have, say after a downgrade.
			s.opts.Logger.Warn("the chosen theme isn't one this server has; using the default", "theme", name)
			s.themeFallbacks.WithLabelValues("unknown").Inc()
		}
	}
	h := w.Header()
	// It differs from one person to the next, so no shared cache may keep it.
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", t.ETag)
	h.Set("Content-Type", "text/css; charset=utf-8")
	http.ServeContent(w, r, "theme.css", time.Time{}, bytes.NewReader(t.CSS))
}

// themeName is the theme the visitor picked, if they're logged in and picked one this
// server has, and the site's otherwise.
func (s *site) themeName(r *http.Request, themes *theme.Set) (string, error) {
	if session, _, err := s.sessionOf(r); err == nil && session.Theme != "" {
		if _, ok := themes.Lookup(session.Theme); ok {
			return session.Theme, nil
		}
		s.opts.Logger.Warn("the user's theme isn't one this server has; using the site's", "theme", session.Theme)
		s.themeFallbacks.WithLabelValues("unknown").Inc()
	}
	return s.opts.Store.GetTheme(r.Context())
}

func newThemeFallbacks(reg prometheus.Registerer) *prometheus.CounterVec {
	return promauto.With(reg).NewCounterVec(prometheus.CounterOpts{
		Name: "andante_web_theme_fallbacks_total",
		Help: "Times the website served the default theme in place of the chosen one, by reason: unreadable or unknown.",
	}, []string{"reason"})
}

func (s *site) setTheme(w http.ResponseWriter, r *http.Request, v visitor) {
	name := r.PostForm.Get("theme")
	if _, ok := theme.MustBuiltin().Lookup(name); !ok {
		s.showUsers(w, r, v, http.StatusUnprocessableEntity, "There's no theme called "+name+".", nil)
		return
	}
	if err := s.opts.Store.SetTheme(r.Context(), name); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), v.session.Name, "settings.changed", "theme."+name, s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

// setMyTheme sets the visitor's own theme, or with an empty name, has them follow the site's.
func (s *site) setMyTheme(w http.ResponseWriter, r *http.Request, v visitor) {
	name := r.PostForm.Get("theme")
	if _, ok := theme.MustBuiltin().Lookup(name); name != "" && !ok {
		s.showAccount(w, r, v, http.StatusUnprocessableEntity, "There's no theme called "+name+".", "", "")
		return
	}
	if _, err := s.opts.Store.SetUserTheme(r.Context(), v.actor, name); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}
