// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/seal"
	"github.com/gerrowadat/andante/internal/stacktest"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/gerrowadat/andante/internal/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

const (
	cookieName = "__Host-andante_session"
	phrase     = "a long enough phrase"
	home       = "203.0.113.1"
	away       = "198.51.100.1"
)

var csrfField = regexp.MustCompile(`name="csrf" value="([0-9a-f]+)"`)

// countingHasher counts how much password work each login does.
type countingHasher struct {
	*password.Hasher
	work atomic.Int32
	err  error
}

func (c *countingHasher) Verify(ctx context.Context, given, encoded string) (bool, error) {
	c.work.Add(1)
	if c.err != nil {
		return false, c.err
	}
	return c.Hasher.Verify(ctx, given, encoded)
}

func (c *countingHasher) Waste(ctx context.Context, given string) error {
	c.work.Add(1)
	if c.err != nil {
		return c.err
	}
	return c.Hasher.Waste(ctx, given)
}

type fixture struct {
	// session is the session cookie's name, which depends on the base URL.
	session  string
	stack    *stacktest.Stack
	store    *store.Store
	hasher   *countingHasher
	registry *prometheus.Registry
	server   *httptest.Server
	oidc     *web.OIDCOptions
	setup    *bootstrap.Service
	// stopping is the server shutting down, as far as the website knows.
	stopping chan struct{}
	mu       sync.Mutex
	now      time.Time
}

func (f *fixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *fixture) serve(t *testing.T, baseURL string, passwordLogin bool) {
	t.Helper()
	f.session = cookieName
	if strings.Count(baseURL, "/") > 2 {
		f.session = "__Secure-andante_session"
	}
	// Everything in these tests comes through a proxy, which is how each one gets to
	// choose where a request comes from.
	clients, err := clientip.New([]string{"127.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	box, err := seal.New(storetest.Secret)
	if err != nil {
		t.Fatal(err)
	}
	f.registry = prometheus.NewRegistry()
	f.stopping = make(chan struct{})
	f.server = httptest.NewServer(web.New(web.Options{
		Store: f.store, Hasher: f.hasher, Clients: clients, BaseURL: baseURL, PasswordLogin: passwordLogin,
		Registry: f.registry, Logger: slog.New(slog.DiscardHandler), Now: f.clock,
		App: f.stack.App, DefaultTimezone: "UTC", Box: box, OIDC: f.oidc, Bootstrap: f.setup, Stopping: f.stopping,
	}))
	t.Cleanup(f.server.Close)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h, err := password.NewHasher(password.Params{MemoryKiB: 8, Passes: 1}, 4)
	if err != nil {
		t.Fatal(err)
	}
	stack := stacktest.New(t)
	f := &fixture{stack: stack, store: stack.Store, hasher: &countingHasher{Hasher: h}, now: time.Now().UTC().Truncate(time.Second)}
	f.serve(t, "https://andante.example", true)
	return f
}

type answer struct {
	status int
	body   string
	header http.Header
	cookie *http.Cookie
	// cookies is every cookie the answer set, the session's included.
	cookies map[string]*http.Cookie
}

func (f *fixture) do(t *testing.T, method, path string, form url.Values, ip, cookie string, headers ...string) answer {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.Header.Set("X-Forwarded-For", ip)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: f.session, Value: cookie})
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	got := answer{status: resp.StatusCode, body: string(data), header: resp.Header, cookies: map[string]*http.Cookie{}}
	for _, c := range resp.Cookies() {
		got.cookies[c.Name] = c
		if c.Name == f.session {
			got.cookie = c
		}
	}
	return got
}

func (f *fixture) login(t *testing.T, name, given, ip string) answer {
	t.Helper()
	return f.do(t, http.MethodPost, "/login", url.Values{"name": {name}, "password": {given}}, ip, "")
}

// sessions checks the live sessions metric, all of them password logins.
func (f *fixture) sessions(t *testing.T, want int) {
	t.Helper()
	expected := fmt.Sprintf(`
		# HELP andante_sessions Live website sessions, by how they logged in: password or oidc.
		# TYPE andante_sessions gauge
		andante_sessions{method="oidc"} 0
		andante_sessions{method="password"} %d
	`, want)
	if err := testutil.GatherAndCompare(f.registry, strings.NewReader(expected), "andante_sessions"); err != nil {
		t.Error(err)
	}
}

// user adds someone who has set a password, and returns their first session.
func (f *fixture) user(t *testing.T, name string) string {
	t.Helper()
	_, raw, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Name: name, Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {phrase}}, home, "")
	if got.status != http.StatusSeeOther || got.cookie == nil {
		t.Fatalf("redeem: %d %s", got.status, got.body)
	}
	return got.cookie.Value
}

func TestLoggingInAndOut(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")

	if got := f.do(t, http.MethodGet, "/", nil, home, ""); got.status != http.StatusSeeOther || got.header.Get("Location") != "/login" {
		t.Errorf("home with no session: %d %s", got.status, got.header.Get("Location"))
	}
	if got := f.do(t, http.MethodGet, "/login", nil, home, ""); got.status != http.StatusOK || !strings.Contains(got.body, `type="password"`) {
		t.Errorf("login page: %d", got.status)
	}

	in := f.login(t, "Dave ", phrase, home)
	if in.status != http.StatusSeeOther || in.header.Get("Location") != "/" || in.cookie == nil {
		t.Fatalf("login: %d %s", in.status, in.body)
	}
	f.sessions(t, 2)
	c := in.cookie
	if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie = %+v", c)
	}

	page := f.do(t, http.MethodGet, "/", nil, home, c.Value)
	if page.status != http.StatusOK || !strings.Contains(page.body, "dave") {
		t.Fatalf("home: %d %s", page.status, page.body)
	}
	match := csrfField.FindStringSubmatch(page.body)
	if match == nil {
		t.Fatalf("no CSRF field: %s", page.body)
	}

	if got := f.do(t, http.MethodPost, "/logout", url.Values{"csrf": {"wrong"}}, home, c.Value); got.status != http.StatusForbidden {
		t.Errorf("logout with a bad CSRF token: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/", nil, home, c.Value); got.status != http.StatusOK {
		t.Errorf("the session ended without a CSRF token: %d", got.status)
	}
	out := f.do(t, http.MethodPost, "/logout", url.Values{"csrf": {match[1]}}, home, c.Value)
	if out.status != http.StatusSeeOther || out.cookie == nil || out.cookie.MaxAge >= 0 {
		t.Errorf("logout: %d %+v", out.status, out.cookie)
	}
	if got := f.do(t, http.MethodGet, "/", nil, home, c.Value); got.status != http.StatusSeeOther {
		t.Errorf("home after logout: %d", got.status)
	}
	f.sessions(t, 1)
	if got := f.do(t, http.MethodPost, "/logout", url.Values{}, home, c.Value); got.status != http.StatusSeeOther {
		t.Errorf("logging out twice: %d", got.status)
	}
}

func TestPlainHTTPUsesAnOrdinaryCookie(t *testing.T) {
	f := newFixture(t)
	f.serve(t, "http://localhost:8080", true)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/login", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS over plain HTTP")
	}
}

// Every way of failing has to look the same, and cost the same.
func TestFailuresLookTheSame(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	f.user(t, "gone")
	if _, _, err := f.store.CreateUser(t.Context(), "nopassword", store.RoleUser); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Pool().Exec(t.Context(), "UPDATE users SET disabled_at = now() WHERE name = 'gone'"); err != nil {
		t.Fatal(err)
	}

	var first *answer
	for i, attempt := range []struct{ name, given string }{
		{"dave", "the wrong phrase"},
		{"nobody", phrase},
		{"nopassword", phrase},
		{"gone", phrase},
	} {
		f.hasher.work.Store(0)
		got := f.login(t, attempt.name, attempt.given, "192.0.2."+strconv.Itoa(i+1))
		if got.status != http.StatusUnauthorized || got.cookie != nil {
			t.Errorf("%s: %d", attempt.name, got.status)
		}
		if first == nil {
			first = &got
		} else if got.body != first.body {
			t.Errorf("%s: the page differs:\n%s\n%s", attempt.name, got.body, first.body)
		}
		if work := f.hasher.work.Load(); work != 1 {
			t.Errorf("%s: %d hashes, want 1, so the time would differ", attempt.name, work)
		}
	}
}

func TestGuessingBacksOff(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	for range 3 {
		if got := f.login(t, "dave", "the wrong phrase", away); got.status != http.StatusUnauthorized {
			t.Fatalf("a free try: %d", got.status)
		}
	}
	// The fourth is the first to earn a wait, and it takes effect on the next.
	if got := f.login(t, "dave", "the wrong phrase", away); got.status != http.StatusUnauthorized {
		t.Fatalf("the fourth: %d", got.status)
	}
	f.hasher.work.Store(0)
	got := f.login(t, "dave", phrase, away)
	if got.status != http.StatusTooManyRequests || got.header.Get("Retry-After") == "" {
		t.Fatalf("while waiting: %d %q", got.status, got.header.Get("Retry-After"))
	}
	if f.hasher.work.Load() != 0 {
		t.Error("a throttled login still hashed")
	}
	f.advance(3 * time.Second)
	if got := f.login(t, "dave", phrase, away); got.status != http.StatusSeeOther {
		t.Errorf("after the wait: %d", got.status)
	}
	want := `
		# HELP andante_logins_total Login attempts, by result.
		# TYPE andante_logins_total counter
		andante_logins_total{result="failed"} 4
		andante_logins_total{result="ok"} 1
		andante_logins_total{result="throttled"} 1
	`
	if err := testutil.GatherAndCompare(f.registry, strings.NewReader(want), "andante_logins_total"); err != nil {
		t.Error(err)
	}
}

// A stranger hammering an account from one address can't stop its owner.
func TestAStrangerCantLockSomeoneOut(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	for range 12 {
		f.login(t, "dave", "the wrong phrase", away)
	}
	if got := f.login(t, "dave", "the wrong phrase", away); got.status != http.StatusTooManyRequests {
		t.Fatalf("the stranger: %d", got.status)
	}
	if got := f.login(t, "dave", phrase, home); got.status != http.StatusSeeOther {
		t.Errorf("the owner, from their usual address: %d", got.status)
	}
	if got := f.login(t, "dave", phrase, "192.0.2.77"); got.status != http.StatusSeeOther {
		t.Errorf("the owner, from a new address: %d", got.status)
	}
}

// Guessing from many addresses is what the limit per account is for. It holds up
// strangers for about a minute, and not the owner's usual address.
func TestGuessingFromEverywhere(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	for i := range 21 {
		f.login(t, "dave", "the wrong phrase", "192.0.2."+strconv.Itoa(i+1))
	}
	got := f.login(t, "dave", "the wrong phrase", "192.0.2.200")
	if got.status != http.StatusTooManyRequests {
		t.Fatalf("after a burst from everywhere: %d", got.status)
	}
	if retry := got.header.Get("Retry-After"); retry != "60" {
		t.Errorf("Retry-After = %q", retry)
	}
	if got := f.login(t, "dave", phrase, home); got.status != http.StatusSeeOther {
		t.Errorf("the owner, from their usual address: %d", got.status)
	}
	f.advance(time.Minute + time.Second)
	if got := f.login(t, "dave", phrase, "192.0.2.201"); got.status != http.StatusSeeOther {
		t.Errorf("a new address, a minute on: %d", got.status)
	}
}

func TestAnAccountThatDoesntExistIsThrottledTheSame(t *testing.T) {
	f := newFixture(t)
	for i := range 21 {
		f.login(t, "nobody", "the wrong phrase", "192.0.2."+strconv.Itoa(i+1))
	}
	if got := f.login(t, "nobody", "the wrong phrase", "192.0.2.200"); got.status != http.StatusTooManyRequests {
		t.Errorf("status = %d", got.status)
	}
}

func TestAPlantedSessionDoesntSurviveLogin(t *testing.T) {
	f := newFixture(t)
	old := f.user(t, "dave")
	req := func() answer {
		form := url.Values{"name": {"dave"}, "password": {phrase}}
		return f.do(t, http.MethodPost, "/login", form, home, old)
	}
	in := req()
	if in.cookie == nil || in.cookie.Value == old {
		t.Fatalf("login kept the session: %+v", in.cookie)
	}
	if got := f.do(t, http.MethodGet, "/", nil, home, old); got.status != http.StatusSeeOther {
		t.Errorf("the old session: %d", got.status)
	}
}

func TestSessionsEnd(t *testing.T) {
	alive := func(t *testing.T, f *fixture, raw string) bool {
		t.Helper()
		return f.do(t, http.MethodGet, "/", nil, home, raw).status == http.StatusOK
	}
	t.Run("idle", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		f.advance(store.SessionIdle - time.Hour)
		if !alive(t, f, raw) {
			t.Fatal("died before the idle limit")
		}
		// That use counts, so the clock starts again.
		f.advance(store.SessionIdle - time.Hour)
		if !alive(t, f, raw) {
			t.Fatal("use didn't extend it")
		}
		f.advance(store.SessionIdle + time.Hour)
		if alive(t, f, raw) {
			t.Error("outlived the idle limit")
		}
	})
	t.Run("absolute", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		for range 5 {
			f.advance(store.SessionIdle - time.Hour)
			alive(t, f, raw)
		}
		if alive(t, f, raw) {
			t.Error("outlived the absolute limit by being used")
		}
	})
	t.Run("revoked", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		user, err := f.store.GetUserByName(t.Context(), "dave")
		if err != nil {
			t.Fatal(err)
		}
		actor := store.Actor{UserID: user.ID}
		sessions, err := f.store.ListSessions(t.Context(), actor, 0, 10, f.clock())
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions = %d, %v", len(sessions), err)
		}
		if sessions[0].Ip != home {
			t.Errorf("address = %q", sessions[0].Ip)
		}
		other := store.Actor{UserID: user.ID + 1, Admin: true}
		if err := f.store.RevokeSession(t.Context(), other, sessions[0].ID, f.clock()); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("someone else revoking it: %v", err)
		}
		if err := f.store.RevokeSession(t.Context(), actor, sessions[0].ID, f.clock()); err != nil {
			t.Fatal(err)
		}
		if alive(t, f, raw) {
			t.Error("outlived being revoked")
		}
		if left, err := f.store.ListSessions(t.Context(), actor, 0, 10, f.clock()); err != nil || len(left) != 0 {
			t.Errorf("sessions left = %d, %v", len(left), err)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		if _, err := f.store.Pool().Exec(t.Context(), "UPDATE users SET disabled_at = now()"); err != nil {
			t.Fatal(err)
		}
		if alive(t, f, raw) {
			t.Error("outlived the user being disabled")
		}
	})
	t.Run("reset", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		if _, err := f.store.ResetUser(t.Context(), "dave"); err != nil {
			t.Fatal(err)
		}
		if alive(t, f, raw) {
			t.Error("outlived a reset")
		}
		if got := f.login(t, "dave", phrase, home); got.status != http.StatusUnauthorized {
			t.Errorf("the old password after a reset: %d", got.status)
		}
	})
	t.Run("survives a restart", func(t *testing.T) {
		f := newFixture(t)
		raw := f.user(t, "dave")
		f.serve(t, "https://andante.example", true)
		if !alive(t, f, raw) {
			t.Error("a restart ended the session")
		}
	})
}

func TestInvites(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	invite, raw, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "alex", Role: store.RoleAdmin}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if !invite.ExpiresAt.Equal(f.clock().Add(store.InviteLife)) {
		t.Errorf("expires = %s", invite.ExpiresAt)
	}
	page := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, "")
	if page.status != http.StatusOK || !strings.Contains(page.body, "alex") {
		t.Fatalf("invite page: %d %s", page.status, page.body)
	}
	for name, candidate := range map[string]string{"short": "short", "common": "password1234", "their name": "alex-alex-alex"} {
		if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {candidate}}, home, ""); got.status != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, got.status)
		}
	}
	used := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {phrase}}, home, "")
	if used.status != http.StatusSeeOther || used.cookie == nil {
		t.Fatalf("redeem: %d %s", used.status, used.body)
	}
	user, err := f.store.GetUserByName(ctx, "alex")
	if err != nil || user.Role != store.RoleAdmin {
		t.Errorf("user = %+v, %v", user, err)
	}
	if user.PasswordHash == nil || strings.Contains(*user.PasswordHash, phrase) {
		t.Error("the password wasn't hashed")
	}
	again := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {"another long phrase"}}, home, "")
	if again.status != http.StatusNotFound {
		t.Errorf("using it twice: %d", again.status)
	}
	if got := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("a used invite's page: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/invite/nonsense", nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("an unknown invite: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/invite/nonsense", url.Values{"password": {phrase}}, home, ""); got.status != http.StatusNotFound {
		t.Errorf("posting to an unknown invite: %d", got.status)
	}
}

func TestInviteRules(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	old := f.user(t, "dave")
	if _, _, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "dave", Role: store.RoleUser}, f.clock()); !errors.Is(err, store.ErrConflict) {
		t.Errorf("an invite for a taken name: %v", err)
	}
	if _, _, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "nobody", Role: store.RoleUser, Reset: true}, f.clock()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a reset for nobody: %v", err)
	}

	_, raw, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "dave", Role: store.RoleAdmin, Reset: true}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	newer := "a different long phrase"
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {newer}}, home, ""); got.status != http.StatusSeeOther {
		t.Fatalf("reset: %d", got.status)
	}
	if got := f.do(t, http.MethodGet, "/", nil, home, old); got.status != http.StatusSeeOther {
		t.Error("the old session outlived a password reset")
	}
	if got := f.login(t, "dave", newer, home); got.status != http.StatusSeeOther {
		t.Errorf("the new password: %d", got.status)
	}
	user, err := f.store.GetUserByName(ctx, "dave")
	if err != nil || user.Role != store.RoleUser {
		t.Errorf("a reset changed the role: %+v, %v", user, err)
	}

	_, expired, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "late", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	f.advance(store.InviteLife + time.Minute)
	if got := f.do(t, http.MethodGet, "/invite/"+expired, nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("an expired invite: %d", got.status)
	}

	// Two invites for one name, where the first gets there first.
	_, one, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "pair", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	_, two, err := f.store.CreateInvite(ctx, store.Holder, store.InviteRequest{Name: "pair", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+one, url.Values{"password": {phrase}}, home, ""); got.status != http.StatusSeeOther {
		t.Fatalf("first: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+two, url.Values{"password": {phrase}}, home, ""); got.status != http.StatusConflict {
		t.Errorf("second: %d", got.status)
	}
}

func TestHeaders(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{"/login", "/", "/invite/nonsense"} {
		got := f.do(t, http.MethodGet, path, nil, home, "")
		for name, want := range map[string]string{
			"Content-Security-Policy":   "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' https: data:; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'",
			"Strict-Transport-Security": "max-age=31536000",
			"X-Content-Type-Options":    "nosniff",
			"Referrer-Policy":           "no-referrer",
			"Cache-Control":             "no-store",
		} {
			if have := got.header.Get(name); have != want {
				t.Errorf("%s: %s = %q", path, name, have)
			}
		}
	}
}

func TestCrossOriginPostsAreRefused(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	form := url.Values{"name": {"dave"}, "password": {phrase}}
	got := f.do(t, http.MethodPost, "/login", form, home, "", "Sec-Fetch-Site", "cross-site")
	if got.status != http.StatusForbidden {
		t.Errorf("cross-site login: %d", got.status)
	}
	if got := f.do(t, http.MethodPost, "/login", form, home, "", "Sec-Fetch-Site", "same-origin"); got.status != http.StatusSeeOther {
		t.Errorf("same-origin login: %d", got.status)
	}
}

func TestPasswordLoginCanBeTurnedOff(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	f.serve(t, "https://andante.example", false)
	if got := f.do(t, http.MethodGet, "/login", nil, home, ""); got.status != http.StatusOK || strings.Contains(got.body, `type="password"`) {
		t.Errorf("GET: %d %s", got.status, got.body)
	}
	if got := f.login(t, "dave", phrase, home); got.status != http.StatusMethodNotAllowed || got.cookie != nil {
		t.Errorf("POST: %d", got.status)
	}
	_, raw, err := f.store.CreateInvite(t.Context(), store.Holder, store.InviteRequest{Name: "alex", Role: store.RoleUser}, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.do(t, http.MethodGet, "/invite/"+raw, nil, home, ""); got.status != http.StatusOK || strings.Contains(got.body, `type="password"`) {
		t.Errorf("the invite page: %d %s", got.status, got.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {phrase}}, home, ""); got.status != http.StatusMethodNotAllowed {
		t.Errorf("setting a password: %d", got.status)
	}
}

func TestABusyServerSaysSo(t *testing.T) {
	f := newFixture(t)
	f.user(t, "dave")
	f.hasher.err = context.DeadlineExceeded
	if got := f.login(t, "dave", phrase, home); got.status != http.StatusServiceUnavailable {
		t.Errorf("status = %d", got.status)
	}
}

func TestOversizedFormsAreRefused(t *testing.T) {
	f := newFixture(t)
	got := f.do(t, http.MethodPost, "/login", url.Values{"name": {strings.Repeat("x", 9<<10)}}, home, "")
	if got.status != http.StatusBadRequest {
		t.Errorf("status = %d", got.status)
	}
}

func TestLoginsAreAudited(t *testing.T) {
	f := newFixture(t)
	raw := f.user(t, "dave")
	f.login(t, "dave", "the wrong phrase", away)
	f.login(t, "nobody", "the wrong phrase", away)
	f.login(t, "dave", phrase, home)
	page := f.do(t, http.MethodGet, "/", nil, home, raw)
	match := csrfField.FindStringSubmatch(page.body)
	if match == nil {
		t.Fatal("no CSRF field")
	}
	f.do(t, http.MethodPost, "/logout", url.Values{"csrf": {match[1]}}, home, raw)

	lines, err := f.store.ListAudit(t.Context(), 0, 20)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range lines {
		got = append(got, l.Who+" "+l.Action+" "+l.Ip)
	}
	want := strings.Join([]string{
		"dave invite.used " + home, "dave login.failed " + away, "unknown login.failed " + away,
		"dave login " + home, "dave logout " + home,
	}, ",")
	if strings.Join(got, ",") != want {
		t.Errorf("audit = %v, want %s", got, want)
	}
	for _, l := range lines {
		if strings.Contains(l.Who+l.Target, "phrase") {
			t.Errorf("a password reached the audit log: %+v", l)
		}
	}
}

// The stylesheet is there for anyone, but not a list of what else is.
func TestStaticFilesWithoutAListing(t *testing.T) {
	f := newFixture(t)
	if got := f.do(t, http.MethodGet, "/static/", nil, home, ""); got.status != http.StatusNotFound {
		t.Errorf("the directory: %d %s", got.status, got.body)
	}
	if got := f.do(t, http.MethodGet, "/static/style.css", nil, home, ""); got.status != http.StatusOK {
		t.Errorf("the stylesheet: %d", got.status)
	}
}

// Under a root path, every link on a page starts with it, and the session cookie is
// kept to it. __Host- needs a Path of /, so the cookie is __Secure- instead.
func TestARootPathReachesLinksAndCookies(t *testing.T) {
	f := newFixture(t)
	f.serve(t, "https://andante.example/feedslow", true)
	f.user(t, "dave")

	login := f.do(t, http.MethodGet, "/login", nil, home, "")
	if !strings.Contains(login.body, `action="/feedslow/login"`) {
		t.Errorf("login page: %s", login.body)
	}
	in := f.login(t, "dave", phrase, home)
	c := in.cookies["__Secure-andante_session"]
	if in.status != http.StatusSeeOther || c == nil {
		t.Fatalf("login: %d %v", in.status, in.cookies)
	}
	if !c.Secure || !c.HttpOnly || c.Path != "/feedslow" || c.Domain != "" {
		t.Errorf("cookie = %+v", c)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Forwarded-For", home)
	req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	page := string(body)
	for _, want := range []string{`href="/feedslow/static/style.css"`, `href="/feedslow/sources"`, `action="/feedslow/logout"`} {
		if !strings.Contains(page, want) {
			t.Errorf("home has no %s: %s", want, page)
		}
	}
	if strings.Contains(page, `href="/sources"`) {
		t.Errorf("home links outside the root: %s", page)
	}
}
