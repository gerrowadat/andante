// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package web is the website: for now, logging in and setting a password. Everything
// here assumes it's on the internet, even though it shouldn't be yet.
package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	cookieName = "andante_session"
	// securePrefix makes a browser refuse the cookie unless it's Secure, has a Path of
	// / and has no Domain, so that a subdomain can't set it.
	securePrefix = "__Host-"

	maxFormBytes  = 8 << 10
	maxNameLength = 64
	maxAgentBytes = 200

	// hashTimeout is how long a login waits for a turn at hashing.
	hashTimeout = 10 * time.Second

	// securityPolicy lets a page do nothing but post forms to this site, use its own
	// stylesheet, and show a source's artwork, which lives on the publisher's site.
	securityPolicy = "default-src 'none'; style-src 'self'; img-src https:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

	hstsAge = "max-age=31536000"

	wrongLogin = "That username and password don't match."
)

// Paths are what this package serves. Everything else on the public listener is
// someone else's.
var Paths = []string{
	"/{$}", "/login", "/logout", "/invite/{token}", "/static/", "/sources", "/sources/", "/reads/", "/tokens/",
	"/add", "/add/", "/users", "/users/", "/account", "/account/",
}

// Hasher hashes and checks passwords.
type Hasher interface {
	Hash(ctx context.Context, password string) (string, error)
	Verify(ctx context.Context, password, encoded string) (bool, error)
	Waste(ctx context.Context, password string) error
}

// Options configure the website.
type Options struct {
	Store         *store.Store
	Hasher        Hasher
	Clients       *clientip.Resolver
	BaseURL       string
	PasswordLogin bool
	Registry      prometheus.Registerer
	Logger        *slog.Logger
	Now           func() time.Time
	// App is what the pages do. DefaultTimezone fills in the pace form.
	App             *app.App
	DefaultTimezone string
}

type site struct {
	opts     Options
	secure   bool
	cookie   string
	throttle *throttle
	jobs     *jobs
	logins   *prometheus.CounterVec
}

// New returns the website's handler.
func New(opts Options) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &site{
		opts:     opts,
		secure:   strings.HasPrefix(opts.BaseURL, "https://"),
		cookie:   cookieName,
		throttle: newThrottle(),
		jobs:     newJobs(),
		logins: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_logins_total",
			Help: "Login attempts, by result.",
		}, []string{"result"}),
	}
	if s.secure {
		s.cookie = securePrefix + cookieName
	}
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.HandleFunc(rt.pattern, s.guard(rt))
	}
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /invite/{token}", s.showInvite)
	mux.HandleFunc("POST /invite/{token}", s.useInvite)
	mux.HandleFunc("POST /logout", s.logout)
	if opts.PasswordLogin {
		mux.HandleFunc("GET /login", s.showLogin)
		mux.HandleFunc("POST /login", s.login)
	}
	return http.NewCrossOriginProtection().Handler(s.headers(mux))
}

func (s *site) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", securityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		// An invite's URL is the credential.
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if s.secure {
			h.Set("Strict-Transport-Security", hstsAge)
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
		next.ServeHTTP(w, r)
	})
}

func (s *site) client(r *http.Request) store.Client {
	agent := r.UserAgent()
	if len(agent) > maxAgentBytes {
		agent = agent[:maxAgentBytes]
	}
	return store.Client{IP: s.opts.Clients.IP(r), UserAgent: agent}
}

func (s *site) fail(w http.ResponseWriter, err error) {
	s.opts.Logger.Error("serving a page failed", "error", err)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}

func (s *site) render(w http.ResponseWriter, status int, page *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := page.Execute(w, data); err != nil {
		s.opts.Logger.Warn("could not write a page", "error", err)
	}
}

func (s *site) audit(ctx context.Context, who, action, target string, client store.Client) {
	entry := store.AuditEntry{Who: who, Action: action, Target: target, IP: client.IP}
	if err := s.opts.Store.Audit(ctx, entry, s.opts.Now()); err != nil {
		s.opts.Logger.Error("could not write the audit log", "error", err)
	}
}

func (s *site) setSession(w http.ResponseWriter, raw string) {
	// Secure is off only for a plain http:// base URL, where a Secure cookie would never
	// come back. The name has the __Host- prefix whenever it's on.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // See above.
		Name: s.cookie, Value: raw, Path: "/", MaxAge: int(store.SessionAbsolute.Seconds()),
		Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (s *site) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // As in setSession.
		Name: s.cookie, Path: "/", MaxAge: -1, Secure: s.secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

func (s *site) sessionOf(r *http.Request) (store.Session, string, error) {
	cookie, err := r.Cookie(s.cookie)
	if err != nil {
		return store.Session{}, "", store.ErrNotFound
	}
	session, err := s.opts.Store.LookupSession(r.Context(), cookie.Value, s.opts.Now())
	return session, cookie.Value, err
}

// csrfFor derives a form token from the session, so that there's nothing to store and
// it's dead when the session is.
func csrfFor(raw string) string {
	mac := hmac.New(sha256.New, []byte(raw))
	mac.Write([]byte("csrf"))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *site) showLogin(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, loginPage, map[string]string{})
}

func (s *site) tooMany(w http.ResponseWriter, wait time.Duration) {
	s.logins.WithLabelValues("throttled").Inc()
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	s.render(w, http.StatusTooManyRequests, loginPage, map[string]string{"Error": "Too many tries. Wait a bit and try again."})
}

func (s *site) refuse(ctx context.Context, w http.ResponseWriter, who string, client store.Client) {
	s.logins.WithLabelValues("failed").Inc()
	s.audit(ctx, who, "login.failed", "", client)
	s.render(w, http.StatusUnauthorized, loginPage, map[string]string{"Error": wrongLogin})
}

// checkPassword does the same work whether or not the account exists and can log in,
// so that neither the answer nor the time it takes gives anything away.
func (s *site) checkPassword(ctx context.Context, name, given string) (int64, string, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, hashTimeout)
	defer cancel()
	user, err := s.opts.Store.GetUserByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) || (err == nil && user.PasswordHash == nil) {
		who := "unknown"
		if err == nil {
			who = user.Name
		}
		return 0, who, false, s.opts.Hasher.Waste(ctx, given)
	}
	if err != nil {
		return 0, "", false, err
	}
	match, err := s.opts.Hasher.Verify(ctx, given, *user.PasswordHash)
	if err != nil {
		return 0, "", false, err
	}
	return user.ID, user.Name, match && user.DisabledAt == nil, nil
}

func (s *site) login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx, client, now := r.Context(), s.client(r), s.opts.Now()
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	if len(name) > maxNameLength {
		name = name[:maxNameLength]
	}
	if wait := s.throttle.wait(client.IP, name, now); wait > 0 {
		s.tooMany(w, wait)
		return
	}
	if wait := s.throttle.accountWait(name, now); wait > 0 {
		known := false
		if user, err := s.opts.Store.GetUserByName(ctx, name); err == nil {
			if known, err = s.opts.Store.HasLoggedInFrom(ctx, user.ID, client.IP, now); err != nil {
				s.fail(w, err)
				return
			}
		}
		if !known {
			s.tooMany(w, wait)
			return
		}
	}

	userID, who, ok, err := s.checkPassword(ctx, name, r.PostForm.Get("password"))
	if err != nil {
		s.opts.Logger.Warn("could not check a password", "error", err)
		http.Error(w, "too busy; try again in a moment", http.StatusServiceUnavailable)
		return
	}
	if !ok {
		s.throttle.failed(client.IP, name, now)
		s.refuse(ctx, w, who, client)
		return
	}

	// A new session each time, so that an ID someone planted before login is no use
	// afterwards.
	if cookie, err := r.Cookie(s.cookie); err == nil {
		if err := s.opts.Store.EndSession(ctx, cookie.Value, now); err != nil {
			s.fail(w, err)
			return
		}
	}
	raw, err := s.opts.Store.CreateSession(ctx, userID, client, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.throttle.succeeded(client.IP, name)
	s.logins.WithLabelValues("ok").Inc()
	s.audit(ctx, who, "login", "", client)
	s.setSession(w, raw)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *site) logout(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	session, raw, err := s.sessionOf(r)
	if errors.Is(err, store.ErrNotFound) {
		s.clearSession(w)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if !hmac.Equal([]byte(csrfFor(raw)), []byte(r.PostForm.Get("csrf"))) {
		http.Error(w, "this page has expired; go back and try again", http.StatusForbidden)
		return
	}
	if err := s.opts.Store.EndSession(r.Context(), raw, s.opts.Now()); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), session.Name, "logout", "", s.client(r))
	s.clearSession(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *site) showInvite(w http.ResponseWriter, r *http.Request) {
	invite, err := s.opts.Store.LookupInvite(r.Context(), r.PathValue("token"), s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, invitePage, map[string]string{"Name": invite.Name})
}

func (s *site) useInvite(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx, raw, client := r.Context(), r.PathValue("token"), s.client(r)
	invite, err := s.opts.Store.LookupInvite(ctx, raw, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	chosen := r.PostForm.Get("password")
	if err := password.Check(chosen, invite.Name); err != nil {
		s.render(w, http.StatusUnprocessableEntity, invitePage, map[string]string{"Name": invite.Name, "Error": err.Error()})
		return
	}
	hashCtx, cancel := context.WithTimeout(ctx, hashTimeout)
	defer cancel()
	hash, err := s.opts.Hasher.Hash(hashCtx, chosen)
	if err != nil {
		s.opts.Logger.Warn("could not hash a password", "error", err)
		http.Error(w, "too busy; try again in a moment", http.StatusServiceUnavailable)
		return
	}
	user, session, err := s.opts.Store.RedeemInvite(ctx, raw, hash, client, s.opts.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrConflict):
		s.render(w, http.StatusConflict, invitePage, map[string]string{"Name": invite.Name, "Error": "That name has been taken."})
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.audit(ctx, user.Name, "invite.used", user.Name, client)
	s.setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
