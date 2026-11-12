// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package web is the website: logging in, and every page. Everything here assumes
// it's on the internet.
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
	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/seal"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/internal/throttle"
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
	// stylesheet and images, and show a source's artwork, which lives on the publisher's
	// site.
	securityPolicy = "default-src 'none'; style-src 'self'; img-src 'self' https:; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

	hstsAge = "max-age=31536000"

	wrongLogin = "That username and password don't match."
)

// Paths are what this package serves. Everything else on the public listener is
// someone else's.
var Paths = []string{
	"/{$}", "/login", "/login/", "/logout", "/invite/{token}", "/invite/{token}/oidc", "/static/", "/sources", "/sources/", "/reads/", "/tokens/",
	"/add", "/add/", "/users", "/users/", "/account", "/account/", "/setup",
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
	// Box seals the cookies a login in two steps carries between them.
	Box *seal.Box
	// OIDC turns on logging in with an OpenID provider. Nil is off.
	OIDC *OIDCOptions
	// Throttle is shared with the API. Nil makes one of its own.
	Throttle *throttle.Throttle
	// Bootstrap makes the first admin. Nil leaves /setup out.
	Bootstrap *bootstrap.Service
}

type site struct {
	opts     Options
	secure   bool
	cookie   string
	throttle *throttle.Throttle
	jobs     *jobs
	logins   *prometheus.CounterVec
	codes    *prometheus.CounterVec
	// oidc is nil when logging in with a provider is off.
	oidc       *oidcLogin
	oidcLogins *prometheus.CounterVec
	inviteUses *prometheus.CounterVec
}

// New returns the website's handler.
func New(opts Options) http.Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Throttle == nil {
		opts.Throttle = throttle.New()
	}
	s := &site{
		opts:     opts,
		secure:   strings.HasPrefix(opts.BaseURL, "https://"),
		cookie:   cookieName,
		throttle: opts.Throttle,
		jobs:     newJobs(),
		logins: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_logins_total",
			Help: "Login attempts, by result.",
		}, []string{"result"}),
		codes: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_two_factor_codes_total",
			Help: "Two-factor codes given, by result: ok, recovery, wrong or reused.",
		}, []string{"result"}),
		oidcLogins: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_oidc_logins_total",
			Help: "Logins with the OpenID provider, by result: ok, created, linked, refused or failed.",
		}, []string{"result"}),
		inviteUses: promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
			Name: "andante_invite_uses_total",
			Help: "Invites used, by result: ok, or refused when the link was used up, withdrawn or out of date.",
		}, []string{"result"}),
	}
	if opts.OIDC != nil {
		s.oidc = newOIDCLogin(*opts.OIDC, opts.BaseURL)
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
	// It's mounted even without passwords, for picking a name before single sign-on.
	mux.HandleFunc("POST /invite/{token}", s.useInvite)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /login", s.showLogin)
	if opts.Bootstrap != nil {
		mux.HandleFunc("GET /setup", s.showSetup)
		mux.HandleFunc("POST /setup", s.setup)
	}
	if opts.PasswordLogin {
		mux.HandleFunc("POST /login", s.login)
		mux.HandleFunc("GET /login/code", s.showCode)
		mux.HandleFunc("POST /login/code", s.enterCode)
	}
	if s.oidc != nil {
		mux.HandleFunc("GET /login/oidc", s.loginWithOIDC)
		mux.HandleFunc("GET "+callbackPath, s.oidcCallback)
		mux.HandleFunc("GET /invite/{token}/oidc", s.inviteWithOIDC)
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

type loginData struct {
	Error          string
	Password, OIDC bool
}

func (s *site) loginData(problem string) loginData {
	return loginData{Error: problem, Password: s.opts.PasswordLogin, OIDC: s.oidc != nil}
}

func (s *site) showLogin(w http.ResponseWriter, _ *http.Request) {
	s.render(w, http.StatusOK, loginPage, s.loginData(""))
}

func (s *site) tooMany(w http.ResponseWriter, page *template.Template, wait time.Duration) {
	s.logins.WithLabelValues("throttled").Inc()
	w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
	s.render(w, http.StatusTooManyRequests, page, s.loginData("Too many tries. Wait a bit and try again."))
}

func (s *site) refuse(ctx context.Context, w http.ResponseWriter, who string, client store.Client) {
	s.logins.WithLabelValues("failed").Inc()
	s.audit(ctx, who, "login.failed", "", client)
	s.render(w, http.StatusUnauthorized, loginPage, s.loginData(wrongLogin))
}

// checkPassword does the same work whether or not the account exists and can log in,
// so that neither the answer nor the time it takes gives anything away.
// It returns the user, or what to call them in the audit log if there's no such user.
func (s *site) checkPassword(ctx context.Context, name, given string) (db.User, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, hashTimeout)
	defer cancel()
	user, err := s.opts.Store.GetUserByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) || (err == nil && user.PasswordHash == nil) {
		if err != nil {
			user = db.User{Name: "unknown"}
		}
		return user, false, s.opts.Hasher.Waste(ctx, given)
	}
	if err != nil {
		return db.User{}, false, err
	}
	match, err := s.opts.Hasher.Verify(ctx, given, *user.PasswordHash)
	if err != nil {
		return db.User{}, false, err
	}
	return user, match && user.DisabledAt == nil, nil
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
	if wait := s.throttle.Wait(client.IP, name, now); wait > 0 {
		s.tooMany(w, loginPage, wait)
		return
	}
	if wait := s.throttle.AccountWait(name, now); wait > 0 {
		known := false
		if user, err := s.opts.Store.GetUserByName(ctx, name); err == nil {
			if known, err = s.opts.Store.HasLoggedInFrom(ctx, user.ID, client.IP, now); err != nil {
				s.fail(w, err)
				return
			}
		}
		if !known {
			s.tooMany(w, loginPage, wait)
			return
		}
	}

	user, ok, err := s.checkPassword(ctx, name, r.PostForm.Get("password"))
	if err != nil {
		s.opts.Logger.Warn("could not check a password", "error", err)
		http.Error(w, "too busy; try again in a moment", http.StatusServiceUnavailable)
		return
	}
	if !ok {
		s.throttle.Failed(client.IP, name, now)
		s.refuse(ctx, w, user.Name, client)
		return
	}
	if user.TotpEnabledAt != nil {
		// The throttle keeps counting until the code is right too.
		if err := s.setSealed(w, pendingCookie, "pending", &pending{User: user.ID, Name: user.Name, Until: now.Add(pendingLife)}); err != nil {
			s.fail(w, err)
			return
		}
		http.Redirect(w, r, "/login/code", http.StatusSeeOther)
		return
	}
	s.startSession(w, r, user.ID, user.Name)
}

// endOldSession ends the session a login was given, so that an ID someone planted
// before login is no use afterwards.
func (s *site) endOldSession(r *http.Request) error {
	if cookie, err := r.Cookie(s.cookie); err == nil {
		return s.opts.Store.EndSession(r.Context(), cookie.Value, s.opts.Now())
	}
	return nil
}

// startSession finishes a password login.
func (s *site) startSession(w http.ResponseWriter, r *http.Request, userID int64, name string) {
	ctx, client, now := r.Context(), s.client(r), s.opts.Now()
	if err := s.endOldSession(r); err != nil {
		s.fail(w, err)
		return
	}
	raw, err := s.opts.Store.CreateSession(ctx, userID, client, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.throttle.Succeeded(client.IP, name)
	s.logins.WithLabelValues("ok").Inc()
	s.audit(ctx, name, "login", "", client)
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
	raw := r.PathValue("token")
	invite, err := s.opts.Store.LookupInvite(r.Context(), raw, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, invitePage, s.inviteData(raw, invite.Name))
}

// pickName checks a name someone picked for themselves on an invite page. It's a
// message for them when the name won't do, and empty when it will.
func (s *site) pickName(ctx context.Context, name string) (string, int, error) {
	if !store.ValidName(name) {
		return "A username is 1 to 64 lower-case letters, digits, dots, dashes or underscores, starting with a letter or digit.", http.StatusUnprocessableEntity, nil
	}
	_, err := s.opts.Store.GetUserByName(ctx, name)
	switch {
	case err == nil:
		return "That name has been taken.", http.StatusConflict, nil
	case errors.Is(err, store.ErrNotFound):
		return "", 0, nil
	}
	return "", 0, err
}

func (s *site) useInvite(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	ctx, raw, client := r.Context(), r.PathValue("token"), s.client(r)
	invite, err := s.opts.Store.LookupInvite(ctx, raw, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		s.inviteUses.WithLabelValues("refused").Inc()
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	data := s.inviteData(raw, invite.Name)
	if invite.Name == "" {
		// An invite without a name asks for one first, and carries it along after.
		name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
		problem, status, err := s.pickName(ctx, name)
		if err != nil {
			s.fail(w, err)
			return
		}
		if problem != "" {
			data.Chosen, data.Error = name, problem
			s.render(w, status, invitePage, data)
			return
		}
		data.Name, data.Picked = name, true
		if !r.PostForm.Has("password") {
			s.render(w, http.StatusOK, invitePage, data)
			return
		}
	}
	if !s.opts.PasswordLogin {
		http.Error(w, "passwords are turned off here", http.StatusMethodNotAllowed)
		return
	}
	chosen := r.PostForm.Get("password")
	if err := password.Check(chosen, data.Name); err != nil {
		data.Error = err.Error()
		s.render(w, http.StatusUnprocessableEntity, invitePage, data)
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
	user, session, err := s.opts.Store.RedeemInvite(ctx, raw, data.Name, hash, client, s.opts.Now())
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.inviteUses.WithLabelValues("refused").Inc()
		http.NotFound(w, r)
		return
	case errors.Is(err, store.ErrConflict):
		picked := data.Name
		data = s.inviteData(raw, invite.Name)
		data.Chosen, data.Error = picked, "That name has been taken."
		s.render(w, http.StatusConflict, invitePage, data)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	s.inviteUses.WithLabelValues("ok").Inc()
	s.audit(ctx, user.Name, "invite.used", user.Name, client)
	s.setSession(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type inviteData struct {
	// Name is who the invite is for, or the name they picked.
	Name, Token, Error string
	// Chosen is a name they tried that won't do.
	Chosen string
	// Picked means they picked Name themselves, so the form carries it along.
	Picked         bool
	Password, OIDC bool
}

func (s *site) inviteData(token, name string) inviteData {
	return inviteData{Name: name, Token: token, Password: s.opts.PasswordLogin, OIDC: s.oidc != nil}
}
