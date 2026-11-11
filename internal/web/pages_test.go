// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/web"
	"github.com/gerrowadat/andante/pkg/client"
)

// person is someone logged in, with what their pages need.
type person struct {
	actor   store.Actor
	session string
	csrf    string
}

func (f *fixture) person(t *testing.T, name string, admin bool) person {
	t.Helper()
	role := store.RoleUser
	if admin {
		role = store.RoleAdmin
	}
	_, raw, err := f.store.CreateInvite(t.Context(), name, role, false, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	got := f.do(t, http.MethodPost, "/invite/"+raw, url.Values{"password": {phrase}}, home, "")
	if got.status != http.StatusSeeOther || got.cookie == nil {
		t.Fatalf("redeem: %d %s", got.status, got.body)
	}
	session, err := f.store.LookupSession(t.Context(), got.cookie.Value, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	page := f.do(t, http.MethodGet, "/", nil, home, got.cookie.Value)
	match := csrfField.FindStringSubmatch(page.body)
	if match == nil {
		t.Fatalf("no form token on the home page: %s", page.body)
	}
	return person{actor: store.Actor{UserID: session.UserID, Admin: admin}, session: got.cookie.Value, csrf: match[1]}
}

func (f *fixture) get(t *testing.T, p person, path string) answer {
	t.Helper()
	got := f.do(t, http.MethodGet, path, nil, home, p.session)
	if strings.Contains(got.body, "<script") {
		t.Errorf("%s has a script, and every page has to work without one", path)
	}
	return got
}

func (f *fixture) post(t *testing.T, p person, path string, form url.Values) answer {
	t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf", p.csrf)
	return f.do(t, http.MethodPost, path, form, home, p.session)
}

// source adds a source with some entries to the server's copy, as if the catalogue
// had found them.
func (f *fixture) source(t *testing.T, title string) int64 {
	t.Helper()
	ctx := t.Context()
	var config client.SourceConfig
	if err := config.FromFeedConfig(client.FeedConfig{Strategy: client.Feed, FeedURL: "https://" + strings.ToLower(strings.ReplaceAll(title, " ", "")) + ".example/rss"}); err != nil {
		t.Fatal(err)
	}
	created, err := f.stack.App.CreateSourceNamed(ctx, replica.SourceCreate{SourceCreate: client.SourceCreate{
		Title: title, HomeURL: "https://comic.example/", DefaultKind: client.Page, Config: config,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var entries []catalogue.NewEntry
	for i := range 5 {
		u := "https://comic.example/" + strconv.Itoa(i)
		title := "Page " + strconv.Itoa(i)
		date := time.Date(2010, 1, 1+7*i, 0, 0, 0, 0, time.UTC)
		entries = append(entries, catalogue.NewEntry{Kind: "page", URL: &u, Title: &title, Date: &date, Images: []string{}, Enclosures: []byte("[]"), Extra: []byte("{}"), ContentHash: []byte(u)})
	}
	err = f.stack.Catalogue.SaveBatch(ctx, entries, nil, catalogue.Outcome{SourceID: created.ID, Cursor: []byte("{}"), State: "tail"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.stack.Sync.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
	return created.ID
}

var csrfJob = regexp.MustCompile(`action="(/add/[0-9a-f]+/0)"`)

func idIn(location, prefix string) int64 {
	id, _ := strconv.ParseInt(strings.TrimPrefix(location, prefix), 10, 64)
	return id
}

// track starts reading a source through the page, and returns the read's ID.
func (f *fixture) track(t *testing.T, p person, source int64) int64 {
	t.Helper()
	got := f.post(t, p, "/sources/"+strconv.FormatInt(source, 10)+"/track", url.Values{
		"name": {"Mornings"}, "preset": {"daily"}, "at": {"07:00"}, "tz": {"UTC"}, "count": {"1"},
	})
	if got.status != http.StatusSeeOther || !strings.HasPrefix(got.header.Get("Location"), "/reads/") {
		t.Fatalf("track: %d %s", got.status, got.body)
	}
	return idIn(got.header.Get("Location"), "/reads/")
}

func path(pattern string, id int64) string {
	_, p, _ := strings.Cut(pattern, " ")
	p = strings.ReplaceAll(p, "{$}", "")
	p = strings.ReplaceAll(p, "{id}", strconv.FormatInt(id, 10))
	p = strings.ReplaceAll(p, "{job}", "nojob")
	return strings.ReplaceAll(p, "{n}", "0")
}

func TestEveryPageNeedsALogin(t *testing.T) {
	f := newFixture(t)
	for _, rt := range web.Routes() {
		method, _, _ := strings.Cut(rt.Pattern, " ")
		got := f.do(t, method, path(rt.Pattern, 1), url.Values{}, home, "")
		switch method {
		case http.MethodGet:
			if got.status != http.StatusSeeOther || got.header.Get("Location") != "/login" {
				t.Errorf("%s with no login: %d, %q", rt.Pattern, got.status, got.header.Get("Location"))
			}
		default:
			if got.status != http.StatusForbidden {
				t.Errorf("%s with no login: %d", rt.Pattern, got.status)
			}
		}
	}
}

func TestEveryFormNeedsItsToken(t *testing.T) {
	f := newFixture(t)
	dave := f.person(t, "dave", true)
	for _, rt := range web.Routes() {
		method, _, _ := strings.Cut(rt.Pattern, " ")
		if method != http.MethodPost {
			continue
		}
		got := f.do(t, method, path(rt.Pattern, 1), url.Values{"csrf": {"0000"}}, home, dave.session)
		if got.status != http.StatusForbidden {
			t.Errorf("%s with a bad form token: %d", rt.Pattern, got.status)
		}
	}
}

func TestAdminPagesAreForAdmins(t *testing.T) {
	f := newFixture(t)
	bob := f.person(t, "bob", false)
	for _, rt := range web.Routes() {
		if !rt.Admin {
			continue
		}
		method, _, _ := strings.Cut(rt.Pattern, " ")
		form := url.Values{}
		if method == http.MethodPost {
			form.Set("csrf", bob.csrf)
		}
		got := f.do(t, method, path(rt.Pattern, bob.actor.UserID), form, home, bob.session)
		if got.status != http.StatusForbidden {
			t.Errorf("%s as a user: %d", rt.Pattern, got.status)
		}
	}
}

// Every route that takes the ID of something a person owns, tried by somebody else.
// The list comes from the pages themselves, so a new one is covered as soon as it
// says what it owns.
func TestNobodyReachesSomeoneElsesThings(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	alice, mallory := f.person(t, "alice", false), f.person(t, "mallory", false)
	source := f.source(t, "Harbour Lights")
	read := f.track(t, alice, source)
	tokens, err := f.store.ListFeedTokens(ctx, alice.actor, read, 0, 10)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("alice's links: %v, %v", tokens, err)
	}
	apiToken, _, err := f.store.CreateAPIToken(ctx, alice.actor, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.store.LookupSession(ctx, alice.session, f.clock())
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{"read": read, "token": tokens[0].ID, "api-token": apiToken.ID, "session": session.ID}

	tried := 0
	for _, rt := range web.Routes() {
		id, owned := ids[rt.Owns]
		if !owned {
			continue
		}
		tried++
		method, _, _ := strings.Cut(rt.Pattern, " ")
		form := url.Values{"seq": {"3"}, "label": {"x"}, "preset": {"daily"}, "at": {"07:00"}, "tz": {"UTC"}}
		var got answer
		if method == http.MethodPost {
			got = f.post(t, mallory, path(rt.Pattern, id), form)
		} else {
			got = f.get(t, mallory, path(rt.Pattern, id))
		}
		if got.status != http.StatusNotFound {
			t.Errorf("%s with alice's %s: %d", rt.Pattern, rt.Owns, got.status)
		}
	}
	if tried < 10 {
		t.Errorf("only %d routes tried; the table has lost its owners", tried)
	}

	// Nothing of alice's moved.
	if _, err := f.store.LookupSession(ctx, alice.session, f.clock()); err != nil {
		t.Errorf("alice's session: %v", err)
	}
	if got, err := f.stack.Reads.Get(ctx, alice.actor, read); err != nil || got.Read.Name != "Mornings" || got.State != "active" {
		t.Errorf("alice's read: %+v, %v", got, err)
	}
	if mine := f.get(t, mallory, "/"); strings.Contains(mine.body, "Mornings") {
		t.Error("mallory's home page shows alice's read")
	}
}

func TestReadingASerial(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	alice := f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	at := "/sources/" + strconv.FormatInt(source, 10)

	if got := f.get(t, alice, "/"); !strings.Contains(got.body, "Nothing yet") {
		t.Errorf("an empty home page: %s", got.body)
	}
	browse := f.get(t, alice, "/sources?q=harbour")
	if browse.status != http.StatusOK || !strings.Contains(browse.body, "Harbour Lights") {
		t.Errorf("browse: %d %s", browse.status, browse.body)
	}
	if got := f.get(t, alice, "/sources?q=nothing-like-it"); strings.Contains(got.body, "Harbour Lights") {
		t.Error("a search that shouldn't match did")
	}
	page := f.get(t, alice, at)
	if page.status != http.StatusOK || !strings.Contains(page.body, "Page 0") || !strings.Contains(page.body, "Track this") {
		t.Errorf("source page: %d %s", page.status, page.body)
	}
	if strings.Contains(page.body, "Delete this source") {
		t.Error("a user is offered deleting a source")
	}

	if got := f.post(t, alice, at+"/track", url.Values{"preset": {"daily"}, "at": {"25:00"}, "tz": {"UTC"}}); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a bad time: %d", got.status)
	}
	for _, form := range []url.Values{
		{"preset": {"daily"}, "at": {"07:00"}, "tz": {"UTC"}, "count": {"4294967297"}},
		{"preset": {"replay"}, "speed": {"7"}, "max_gap": {"99999999999"}},
		{"preset": {"replay"}, "speed": {"7"}, "max_gap": {"3651"}},
	} {
		if got := f.post(t, alice, at+"/track", form); got.status != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", form, got.status)
		}
	}
	read := f.track(t, alice, source)
	r := "/reads/" + strconv.FormatInt(read, 10)
	if got := f.post(t, alice, r+"/jump", url.Values{"seq": {"4294967299"}}); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a jump past int32: %d", got.status)
	}
	shown := f.get(t, alice, r)
	for _, want := range []string{"Every day at 07:00 (UTC)", "1 of 5 released", "/f/"} {
		if !strings.Contains(shown.body, want) {
			t.Errorf("the read's page has no %q", want)
		}
	}
	if home := f.get(t, alice, "/"); !strings.Contains(home.body, "Mornings") {
		t.Error("the home page doesn't list the read")
	}

	// A link for a friend, which works as a feed.
	if got := f.post(t, alice, r+"/tokens", url.Values{"label": {"Alex"}}); got.status != http.StatusSeeOther {
		t.Fatalf("add a link: %d %s", got.status, got.body)
	}
	tokens, err := f.store.ListFeedTokens(ctx, alice.actor, read, 0, 10)
	if err != nil || len(tokens) != 2 || tokens[1].Role != feed.RoleViewer {
		t.Fatalf("links: %+v, %v", tokens, err)
	}

	for _, step := range []struct{ path, want string }{
		{r + "/skip", "2 of 5"},
		{r + "/pause", "paused"},
		{r + "/resume", "2 of 5"},
	} {
		if got := f.post(t, alice, step.path, nil); got.status != http.StatusSeeOther {
			t.Fatalf("%s: %d %s", step.path, got.status, got.body)
		}
		if got := f.get(t, alice, r); !strings.Contains(got.body, step.want) {
			t.Errorf("after %s the page has no %q", step.path, step.want)
		}
	}
	if got := f.post(t, alice, r+"/jump", url.Values{"seq": {"3"}}); got.status != http.StatusSeeOther {
		t.Errorf("jump: %d %s", got.status, got.body)
	}
	if got := f.post(t, alice, r+"/jump", url.Values{"seq": {"1"}}); got.status != http.StatusConflict {
		t.Errorf("a jump backwards: %d", got.status)
	}
	if got := f.post(t, alice, r+"/pace", url.Values{"preset": {"replay"}, "speed": {"7"}, "max_gap": {"3"}}); got.status != http.StatusSeeOther {
		t.Fatalf("repace: %d %s", got.status, got.body)
	}
	if got := f.get(t, alice, r); !strings.Contains(got.body, "The original timing, 7 times as fast") {
		t.Errorf("the new pace isn't shown: %s", got.body)
	}

	if got := f.post(t, alice, "/tokens/"+strconv.FormatInt(tokens[1].ID, 10)+"/revoke", url.Values{"read": {strconv.FormatInt(read, 10)}}); got.status != http.StatusSeeOther || got.header.Get("Location") != r {
		t.Errorf("stop a link: %d %q", got.status, got.header.Get("Location"))
	}
	if got := f.get(t, alice, r+"/delete"); got.status != http.StatusOK || !strings.Contains(got.body, "Delete it") {
		t.Errorf("the delete page: %d", got.status)
	}
	if got := f.post(t, alice, r+"/delete", nil); got.status != http.StatusSeeOther {
		t.Errorf("delete: %d", got.status)
	}
	if _, err := f.stack.Reads.Get(ctx, alice.actor, read); err == nil {
		t.Error("the read survived deleting it")
	}
}

func TestBrowsingNeverAsksTheCatalogue(t *testing.T) {
	f := newFixture(t)
	alice := f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	f.track(t, alice, source)
	f.stack.CatalogueServer.Close()
	for _, p := range []string{"/", "/sources", "/sources/" + strconv.FormatInt(source, 10)} {
		if got := f.get(t, alice, p); got.status != http.StatusOK {
			t.Errorf("%s with the catalogue down: %d", p, got.status)
		}
	}
}

func TestAdminsManageSources(t *testing.T) {
	f := newFixture(t)
	dave, alice := f.person(t, "dave", true), f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	at := "/sources/" + strconv.FormatInt(source, 10)
	if got := f.get(t, dave, at); !strings.Contains(got.body, "Delete this source") {
		t.Error("an admin isn't offered deleting a source")
	}
	if got := f.post(t, dave, at+"/edit", url.Values{"title": {"Harbour Lights Remastered"}, "kind": {"page"}, "state": {"indexing"}}); got.status != http.StatusSeeOther {
		t.Fatalf("edit: %d %s", got.status, got.body)
	}
	if got := f.get(t, alice, at); !strings.Contains(got.body, "Harbour Lights Remastered") {
		t.Error("the change didn't show")
	}

	read := f.track(t, alice, source)
	if got := f.post(t, dave, at+"/delete", nil); got.status != http.StatusConflict {
		t.Errorf("deleting a source someone reads: %d", got.status)
	}
	if got := f.post(t, alice, "/reads/"+strconv.FormatInt(read, 10)+"/delete", nil); got.status != http.StatusSeeOther {
		t.Fatalf("alice's delete: %d", got.status)
	}
	if got := f.post(t, dave, at+"/delete", nil); got.status != http.StatusSeeOther {
		t.Errorf("deleting a source nobody reads: %d %s", got.status, got.body)
	}
}

func TestAddingASite(t *testing.T) {
	f := newFixture(t)
	alice := f.person(t, "alice", false)
	site := sitetest.Serve(t, "podcast")

	if got := f.post(t, alice, "/add", url.Values{"url": {"not a url"}}); got.status != http.StatusUnprocessableEntity {
		t.Errorf("a bad address: %d", got.status)
	}
	look := func(form url.Values) answer {
		t.Helper()
		started := f.post(t, alice, "/add", form)
		if started.status != http.StatusSeeOther {
			t.Fatalf("start: %d %s", started.status, started.body)
		}
		at := started.header.Get("Location")
		deadline := time.Now().Add(30 * time.Second)
		for {
			got := f.get(t, alice, at)
			if !strings.Contains(got.body, "http-equiv=\"refresh\"") {
				return got
			}
			if time.Now().After(deadline) {
				t.Fatal("still looking")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	found := look(url.Values{"url": {site.URL + "/feed.xml"}})
	if !strings.Contains(found.body, "Add this") || !strings.Contains(found.body, "Episode 1") {
		t.Fatalf("candidates: %s", found.body)
	}
	job := csrfJob.FindStringSubmatch(found.body)
	if job == nil {
		t.Fatalf("no candidate form: %s", found.body)
	}
	added := f.post(t, alice, job[1], nil)
	if added.status != http.StatusSeeOther || !strings.HasSuffix(added.header.Get("Location"), "/track") {
		t.Fatalf("add: %d %q %s", added.status, added.header.Get("Location"), added.body)
	}

	// The second time, it's already here.
	again := look(url.Values{"url": {site.URL + "/feed.xml"}})
	if !strings.Contains(again.body, "already here") {
		t.Errorf("a second look didn't find the source: %s", again.body)
	}

	// Somebody else's look is nothing to anyone else.
	mallory := f.person(t, "mallory", false)
	if got := f.get(t, mallory, strings.TrimSuffix(job[1], "/0")); got.status != http.StatusNotFound {
		t.Errorf("mallory looking at alice's search: %d", got.status)
	}
}

func TestManagingUsersFromThePage(t *testing.T) {
	f := newFixture(t)
	dave, bob := f.person(t, "dave", true), f.person(t, "bob", false)

	if got := f.post(t, dave, "/users/invite", url.Values{"name": {"carol"}}); got.status != http.StatusOK || !strings.Contains(got.body, "/invite/") {
		t.Errorf("invite: %d %s", got.status, got.body)
	}
	if got := f.post(t, dave, "/users/invite", url.Values{"name": {"nobody"}, "reset": {"1"}}); got.status != http.StatusNotFound {
		t.Errorf("a password link for nobody: %d", got.status)
	}
	reset := f.post(t, dave, "/users/invite", url.Values{"name": {"bob"}, "reset": {"1"}})
	link := regexp.MustCompile(`/invite/([^<"\s]+)`).FindStringSubmatch(reset.body)
	if reset.status != http.StatusOK || link == nil {
		t.Fatalf("a new password link: %d %s", reset.status, reset.body)
	}
	if got := f.do(t, http.MethodPost, "/invite/"+link[1], url.Values{"password": {"a different phrase"}}, away, ""); got.status != http.StatusSeeOther {
		t.Fatalf("using it: %d %s", got.status, got.body)
	}
	if got := f.get(t, bob, "/"); got.status != http.StatusSeeOther {
		t.Errorf("bob's old session outlived a password reset: %d", got.status)
	}
	bob = f.person(t, "bobby", false)
	u := "/users/" + strconv.FormatInt(bob.actor.UserID, 10)
	if got := f.post(t, dave, u, url.Values{"action": {"disable"}}); got.status != http.StatusSeeOther {
		t.Fatalf("disable: %d %s", got.status, got.body)
	}
	if got := f.get(t, bob, "/"); got.status != http.StatusSeeOther {
		t.Errorf("a disabled user's session still works: %d", got.status)
	}
	if got := f.post(t, dave, u, url.Values{"action": {"enable"}}); got.status != http.StatusSeeOther {
		t.Errorf("enable: %d", got.status)
	}
	if got := f.post(t, dave, "/users/"+strconv.FormatInt(dave.actor.UserID, 10), url.Values{"action": {"user"}}); got.status != http.StatusConflict {
		t.Errorf("the last admin stepping down: %d", got.status)
	}
	if got := f.get(t, dave, u+"/delete"); !strings.Contains(got.body, "be undone") {
		t.Errorf("the delete page doesn't warn: %s", got.body)
	}
	if got := f.post(t, dave, u+"/delete", nil); got.status != http.StatusSeeOther {
		t.Errorf("delete: %d %s", got.status, got.body)
	}
	if got := f.get(t, dave, "/users"); strings.Contains(got.body, ">bobby<") {
		t.Error("bobby is still listed")
	}
}

func TestTheAccountPage(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	alice := f.person(t, "alice", false)
	other := f.login(t, "alice", phrase, away)
	if other.cookie == nil {
		t.Fatalf("a second login: %d", other.status)
	}

	if got := f.post(t, alice, "/account/password", url.Values{"current": {"wrong"}, "password": {"another long phrase"}}); got.status != http.StatusUnauthorized {
		t.Errorf("the wrong current password: %d", got.status)
	}
	if got := f.post(t, alice, "/account/password", url.Values{"current": {phrase}, "password": {"another long phrase"}}); got.status != http.StatusOK {
		t.Fatalf("change password: %d %s", got.status, got.body)
	}
	if _, err := f.store.LookupSession(ctx, other.cookie.Value, f.clock()); err == nil {
		t.Error("the other session outlived a password change")
	}
	if _, err := f.store.LookupSession(ctx, alice.session, f.clock()); err != nil {
		t.Errorf("this session ended: %v", err)
	}
	if got := f.login(t, "alice", "another long phrase", home); got.status != http.StatusSeeOther {
		t.Errorf("logging in with the new password: %d", got.status)
	}

	made := f.post(t, alice, "/account/tokens", url.Values{"label": {"laptop"}})
	if made.status != http.StatusOK || !strings.Contains(made.body, "only shown once") {
		t.Fatalf("make a token: %d %s", made.status, made.body)
	}
	tokens, err := f.store.ListAPITokens(ctx, alice.actor, 0, 10)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("tokens: %v, %v", tokens, err)
	}
	if got := f.post(t, alice, "/account/tokens/"+strconv.FormatInt(tokens[0].ID, 10)+"/revoke", nil); got.status != http.StatusConflict {
		t.Errorf("revoking the last API token: %d", got.status)
	}
}

func TestTheReadPageShowsReleasesAsTheyHappen(t *testing.T) {
	f := newFixture(t)
	alice := f.person(t, "alice", false)
	source := f.source(t, "Harbour Lights")
	view, err := f.stack.App.CreateRead(t.Context(), reads.CreateParams{
		UserID: alice.actor.UserID, SourceID: source, Name: "Weekly", Initial: 2,
		Pacing: schedule.RRulePacing{RRule: "FREQ=WEEKLY;BYDAY=SA;BYHOUR=9;BYMINUTE=0;BYSECOND=0", TimeZone: "Europe/Dublin", PerRelease: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := f.get(t, alice, "/reads/"+strconv.FormatInt(view.Read.ID, 10))
	if !strings.Contains(got.body, "Saturdays at 09:00 (Europe/Dublin)") || !strings.Contains(got.body, "2 of 5") {
		t.Errorf("the read's page: %s", got.body)
	}
}
