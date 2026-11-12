// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"crypto/hmac"
	"embed"
	"errors"
	"html/template"
	"io/fs"
	"net/http"
	"strconv"
	"time"

	"github.com/gerrowadat/andante/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

//go:embed static/*
var staticFiles embed.FS

// view parses a page with the layout and the pace form, which several pages share.
func view(name string) *template.Template {
	return template.Must(template.New("layout").ParseFS(templateFiles,
		"templates/layout.html", "templates/pace.html", "templates/"+name+".html"))
}

var (
	homeView      = view("home")
	browseView    = view("browse")
	sourceView    = view("source")
	trackView     = view("track")
	readView      = view("read")
	repaceView    = view("repace")
	confirmView   = view("confirm")
	addView       = view("add")
	foundView     = view("found")
	usersView     = view("users")
	accountView   = view("account")
	twoFactorView = view("two_factor")
	recoveryView  = view("recovery")
	invitesView   = view("invites")
)

// owns says what a route's ID belongs to, so that a test can try every route with
// somebody else's.
type owns string

// What a route's {id} can be.
const (
	ownsNothing  owns = ""
	ownsRead     owns = "read"
	ownsToken    owns = "token"
	ownsSession  owns = "session"
	ownsAPIToken owns = "api-token"
	ownsSource   owns = "source"
	ownsUser     owns = "user"
	ownsInvite   owns = "invite"
)

// visitor is someone logged in.
type visitor struct {
	session store.Session
	raw     string
	actor   store.Actor
}

type route struct {
	pattern string
	admin   bool
	// setup routes are the only ones open to someone who has to set up two-factor.
	setup  bool
	owns   owns
	handle func(*site, http.ResponseWriter, *http.Request, visitor)
}

// RouteInfo describes a page, for tests.
type RouteInfo struct {
	Pattern string
	Admin   bool
	Owns    string
}

// Routes lists every page that needs a login.
func Routes() []RouteInfo {
	out := make([]RouteInfo, 0, len(routes))
	for _, r := range routes {
		out = append(out, RouteInfo{Pattern: r.pattern, Admin: r.admin, Owns: string(r.owns)})
	}
	return out
}

var routes = []route{
	{pattern: "GET /{$}", handle: (*site).home},
	{pattern: "GET /sources", handle: (*site).browse},
	{pattern: "GET /sources/{id}", owns: ownsSource, handle: (*site).source},
	{pattern: "POST /sources/{id}/crawl", owns: ownsSource, handle: (*site).crawl},
	{pattern: "POST /sources/{id}/edit", admin: true, owns: ownsSource, handle: (*site).editSource},
	{pattern: "GET /sources/{id}/delete", admin: true, owns: ownsSource, handle: (*site).confirmDeleteSource},
	{pattern: "POST /sources/{id}/delete", admin: true, owns: ownsSource, handle: (*site).deleteSource},
	{pattern: "GET /sources/{id}/track", owns: ownsSource, handle: (*site).showTrack},
	{pattern: "POST /sources/{id}/track", owns: ownsSource, handle: (*site).track},
	{pattern: "GET /reads/{id}", owns: ownsRead, handle: (*site).read},
	{pattern: "POST /reads/{id}/pause", owns: ownsRead, handle: (*site).pause},
	{pattern: "POST /reads/{id}/resume", owns: ownsRead, handle: (*site).resume},
	{pattern: "POST /reads/{id}/skip", owns: ownsRead, handle: (*site).skip},
	{pattern: "POST /reads/{id}/jump", owns: ownsRead, handle: (*site).jump},
	{pattern: "GET /reads/{id}/pace", owns: ownsRead, handle: (*site).showRepace},
	{pattern: "POST /reads/{id}/pace", owns: ownsRead, handle: (*site).repace},
	{pattern: "GET /reads/{id}/delete", owns: ownsRead, handle: (*site).confirmDeleteRead},
	{pattern: "POST /reads/{id}/delete", owns: ownsRead, handle: (*site).deleteRead},
	{pattern: "POST /reads/{id}/tokens", owns: ownsRead, handle: (*site).addToken},
	{pattern: "POST /tokens/{id}/revoke", owns: ownsToken, handle: (*site).revokeToken},
	{pattern: "GET /add", handle: (*site).showAdd},
	{pattern: "POST /add", handle: (*site).startAdd},
	{pattern: "GET /add/{job}", handle: (*site).found},
	{pattern: "POST /add/{job}/{n}", handle: (*site).addCandidate},
	{pattern: "GET /users", admin: true, handle: (*site).users},
	{pattern: "POST /users/invite", admin: true, handle: (*site).resetLink},
	{pattern: "POST /users/invite-settings", admin: true, handle: (*site).inviteSettings},
	{pattern: "GET /invites", handle: (*site).invites},
	{pattern: "POST /invites", handle: (*site).createInvite},
	{pattern: "POST /invites/{id}/withdraw", owns: ownsInvite, handle: (*site).withdrawInvite},
	{pattern: "POST /users/{id}", admin: true, owns: ownsUser, handle: (*site).changeUser},
	{pattern: "GET /users/{id}/delete", admin: true, owns: ownsUser, handle: (*site).confirmDeleteUser},
	{pattern: "POST /users/{id}/delete", admin: true, owns: ownsUser, handle: (*site).deleteUser},
	{pattern: "GET /account", handle: (*site).account},
	{pattern: "POST /account/password", handle: (*site).changePassword},
	{pattern: "POST /account/sessions/{id}/end", owns: ownsSession, handle: (*site).endSession},
	{pattern: "POST /account/tokens", handle: (*site).addAPIToken},
	{pattern: "POST /account/tokens/{id}/revoke", owns: ownsAPIToken, handle: (*site).revokeAPIToken},
	{pattern: "GET /account/two-factor", setup: true, handle: (*site).twoFactorSetup},
	{pattern: "GET /account/two-factor/qr", setup: true, handle: (*site).twoFactorQR},
	{pattern: "POST /account/two-factor", setup: true, handle: (*site).enableTwoFactor},
	{pattern: "POST /account/two-factor/off", handle: (*site).disableTwoFactor},
	{pattern: "POST /users/two-factor", admin: true, handle: (*site).requireTwoFactor},
	{pattern: "POST /users/{id}/two-factor/off", admin: true, owns: ownsUser, handle: (*site).resetTwoFactor},
}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(w, r)
	})
}

// pageData is what every logged-in page gets, around its own data.
type pageData struct {
	Title, Me, CSRF, Error, Notice string
	Admin, CanInvite               bool
	Refresh                        int
	Data                           any
}

func (s *site) show(w http.ResponseWriter, v visitor, status int, t *template.Template, p pageData) {
	p.Me, p.Admin, p.CanInvite, p.CSRF = v.session.Name, v.actor.Admin, v.session.CanInvite, csrfFor(v.raw)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := t.ExecuteTemplate(w, "layout", p); err != nil {
		s.opts.Logger.Warn("could not write a page", "error", err)
	}
}

// guard is every logged-in page's front door: a session, a form token on a POST, and
// an admin for an admin's page.
func (s *site) guard(rt route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, raw, err := s.sessionOf(r)
		switch {
		case errors.Is(err, store.ErrNotFound) && r.Method == http.MethodGet:
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		case errors.Is(err, store.ErrNotFound):
			http.Error(w, "you've been logged out; log in and try again", http.StatusForbidden)
			return
		case err != nil:
			s.fail(w, err)
			return
		}
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
				return
			}
			if !hmac.Equal([]byte(csrfFor(raw)), []byte(r.PostForm.Get("csrf"))) {
				http.Error(w, "this page has expired; go back and try again", http.StatusForbidden)
				return
			}
		}
		v := visitor{session: session, raw: raw, actor: store.Actor{UserID: session.UserID, Admin: session.Role == store.RoleAdmin, Name: session.Name}}
		if v.session.NeedsTOTP && !rt.setup {
			if r.Method == http.MethodGet {
				http.Redirect(w, r, "/account/two-factor", http.StatusSeeOther)
				return
			}
			http.Error(w, "set up two-factor first", http.StatusForbidden)
			return
		}
		if rt.admin && !v.actor.Admin {
			http.Error(w, "only an admin can do this", http.StatusForbidden)
			return
		}
		rt.handle(s, w, r, v)
	}
}

func idOf(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2 Jan 2006, 15:04 UTC")
}

type when struct{ Machine, Human string }

func whenOf(t *time.Time) *when {
	if t == nil {
		return nil
	}
	return &when{Machine: t.UTC().Format(time.RFC3339), Human: stamp(t)}
}
