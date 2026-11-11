// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// A login in two steps carries what the first step found in a cookie sealed with
// --secret, so that there's nothing to store and the browser can't change it.
const (
	pendingCookie = "andante_pending"
	oidcCookie    = "andante_oidc"
	pendingLife   = 10 * time.Minute
)

func (s *site) cookieNamed(name string) string {
	if s.secure {
		return securePrefix + name
	}
	return name
}

func (s *site) setSealed(w http.ResponseWriter, name, purpose string, v any) error {
	plain, err := json.Marshal(v)
	if err != nil {
		return err
	}
	sealed, err := s.opts.Box.Seal(plain, "web "+purpose)
	if err != nil {
		return err
	}
	// As in setSession. Lax, because the provider sends the browser back with a
	// top-level GET from another site.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // See setSession.
		Name: s.cookieNamed(name), Value: base64.RawURLEncoding.EncodeToString(sealed), Path: "/",
		MaxAge: int(pendingLife.Seconds()), Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

// openSealed reads what setSealed wrote into v, which must have an Until field that
// hasn't passed.
func (s *site) openSealed(r *http.Request, name, purpose string, v interface{ until() time.Time }) bool {
	cookie, err := r.Cookie(s.cookieNamed(name))
	if err != nil {
		return false
	}
	sealed, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return false
	}
	plain, err := s.opts.Box.Open(sealed, "web "+purpose)
	if err != nil {
		return false
	}
	if err := json.Unmarshal(plain, v); err != nil {
		return false
	}
	return s.opts.Now().Before(v.until())
}

func (s *site) clearSealed(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // See setSession.
		Name: s.cookieNamed(name), Path: "/", MaxAge: -1, Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}
