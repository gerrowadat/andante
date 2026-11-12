// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/store"
)

type setupData struct {
	Name, Error string
}

// showSetup is where the first admin starts, with the bootstrap token. It's a 404
// whenever there's nothing to set up, and says nothing about why.
func (s *site) showSetup(w http.ResponseWriter, r *http.Request) {
	open, err := s.opts.Bootstrap.Open(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	if !open {
		http.NotFound(w, r)
		return
	}
	s.render(w, http.StatusOK, setupPage, setupData{})
}

// setup checks the token and hands over to the invite page, which sets a password or
// links an identity at the provider, the same as for anyone else.
func (s *site) setup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	data := setupData{Name: name}
	raw, err := s.opts.Bootstrap.Invite(r.Context(), r.PostForm.Get("token"), name, s.client(r).IP)
	var throttled *bootstrap.ThrottledError
	switch {
	case err == nil:
		// raw is a token the store made, so this stays on the site.
		http.Redirect(w, r, "/invite/"+url.PathEscape(raw), http.StatusSeeOther)
		return
	case errors.Is(err, bootstrap.ErrClosed):
		http.NotFound(w, r)
		return
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(throttled.Wait.Seconds()))))
		data.Error = "Too many tries. Wait a bit and try again."
		s.render(w, http.StatusTooManyRequests, setupPage, data)
	case errors.Is(err, bootstrap.ErrWrongToken):
		data.Error = "That isn't the bootstrap token."
		s.render(w, http.StatusUnauthorized, setupPage, data)
	case errors.Is(err, store.ErrConflict):
		data.Error = "There's already someone called " + name + ". Pick another name. They can be made an admin afterwards."
		s.render(w, http.StatusConflict, setupPage, data)
	case errors.Is(err, store.ErrBadName):
		data.Error = "A name is 1 to 64 lower-case letters, digits, dots, dashes or underscores."
		s.render(w, http.StatusUnprocessableEntity, setupPage, data)
	default:
		s.fail(w, err)
	}
}
