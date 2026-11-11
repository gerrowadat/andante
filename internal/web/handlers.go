// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/password"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/schedule"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/gerrowadat/andante/pkg/pacing"
)

const (
	listSize   = 50
	sampleSize = 10
	maxReads   = 500
)

var kinds = []string{string(client.Page), string(client.Episode), string(client.Article)}

func text(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// webOnly keeps a link on the web. Sources' addresses come from other people's pages.
func webOnly(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return raw
}

// readRow is a read, as the home page and a read's page show it.
type readRow struct {
	ID                int64
	Name, Source      string
	Pace, State       string
	Released, Indexed int
	Next              *when
}

func rowOf(v reads.View, sourceTitle string) readRow {
	start := int(v.Read.StartSeq)
	row := readRow{
		ID: v.Read.ID, Name: v.Read.Name, Source: sourceTitle, Pace: schedule.Describe(v.Pacing), State: v.State,
		Released: max(v.Position-start+1, 0), Next: whenOf(v.NextRelease),
	}
	if v.IndexEnd != nil {
		row.Indexed = max(*v.IndexEnd-start+1, 0)
	}
	return row
}

func (s *site) home(w http.ResponseWriter, r *http.Request, v visitor) {
	ctx := r.Context()
	views, err := s.opts.App.Reads.List(ctx, v.actor, false, 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	rows := make([]readRow, 0, len(views))
	for _, view := range views {
		source, err := s.opts.Store.GetSource(ctx, view.Read.SourceID)
		if err != nil {
			s.fail(w, err)
			return
		}
		rows = append(rows, rowOf(view, source.Title))
	}
	s.show(w, v, http.StatusOK, homeView, pageData{Title: "Your reads", Data: rows})
}

type sourceRow struct {
	ID                          int64
	Title, Kind, State, Checked string
}

// browse lists sources from the server's copy. It never calls the catalogue.
func (s *site) browse(w http.ResponseWriter, r *http.Request, v visitor) {
	q, kind := strings.TrimSpace(r.URL.Query().Get("q")), r.URL.Query().Get("kind")
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	sources, err := s.opts.Store.ListSources(r.Context(), store.SourceFilter{Query: q, Kind: kind}, max(after, 0), listSize+1)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := struct {
		Query, Kind string
		Kinds       []string
		Sources     []sourceRow
		Next        string
	}{Query: q, Kind: kind, Kinds: kinds}
	if len(sources) > listSize {
		sources = sources[:listSize]
		next := url.Values{"after": {strconv.FormatInt(sources[listSize-1].ID, 10)}, "q": {q}, "kind": {kind}}
		data.Next = "/sources?" + next.Encode()
	}
	for _, src := range sources {
		data.Sources = append(data.Sources, sourceRow{ID: src.ID, Title: src.Title, Kind: src.DefaultKind, State: src.State, Checked: stamp(src.LastCrawledAt)})
	}
	s.show(w, v, http.StatusOK, browseView, pageData{Title: "Browse", Data: data})
}

func (s *site) sourceOf(w http.ResponseWriter, r *http.Request) (db.Source, bool) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return db.Source{}, false
	}
	source, err := s.opts.Store.GetSource(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return db.Source{}, false
	}
	if err != nil {
		s.fail(w, err)
		return db.Source{}, false
	}
	return source, true
}

type entryRow struct{ Title, Url, Date string }

func (s *site) source(w http.ResponseWriter, r *http.Request, v visitor) {
	s.showSource(w, r, v, http.StatusOK, "", "")
}

func (s *site) showSource(w http.ResponseWriter, r *http.Request, v visitor, status int, problem, notice string) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	entries, err := s.opts.Store.ListEntries(ctx, source.ID, -1<<31, sampleSize)
	if err != nil {
		s.fail(w, err)
		return
	}
	mine, err := s.opts.Store.ListReads(ctx, v.actor, false, 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := struct {
		Source      db.Source
		Home, Image string
		Checked     string
		Parked      bool
		Entries     []entryRow
		Mine        []db.Read
		Kinds       []string
	}{
		Source: source, Home: webOnly(source.HomeUrl), Image: webOnly(text(source.ImageUrl)), Checked: stamp(source.LastCrawledAt),
		Parked: source.State == "tail" && source.DueAt == nil, Kinds: kinds,
	}
	for _, e := range entries {
		title := text(e.Title)
		if title == "" {
			title = "Entry " + strconv.Itoa(int(e.Seq))
		}
		data.Entries = append(data.Entries, entryRow{Title: title, Url: webOnly(text(e.Url)), Date: stamp(e.OriginalDate)})
	}
	for _, read := range mine {
		if read.SourceID == source.ID {
			data.Mine = append(data.Mine, read)
		}
	}
	s.show(w, v, status, sourceView, pageData{Title: source.Title, Error: problem, Notice: notice, Data: data})
}

func (s *site) crawl(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	if err := s.opts.App.RequestCrawl(r.Context(), source.ID); err != nil {
		s.showSource(w, r, v, http.StatusServiceUnavailable, "That didn't work: "+err.Error(), "")
		return
	}
	s.showSource(w, r, v, http.StatusOK, "", "It'll be checked shortly.")
}

func (s *site) editSource(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	var patch client.SourcePatch
	if title := strings.TrimSpace(r.PostForm.Get("title")); title != "" {
		patch.Title = &title
	}
	if kind := client.Kind(r.PostForm.Get("kind")); kind.Valid() {
		patch.DefaultKind = &kind
	}
	if state := client.SourcePatchState(r.PostForm.Get("state")); state.Valid() {
		patch.State = &state
	}
	if _, err := s.opts.App.UpdateSource(r.Context(), v.actor, source.ID, patch); err != nil {
		s.showSource(w, r, v, http.StatusUnprocessableEntity, "That didn't work: "+err.Error(), "")
		return
	}
	s.audit(r.Context(), v.session.Name, "source.changed", source.Slug, s.client(r))
	http.Redirect(w, r, "/sources/"+strconv.FormatInt(source.ID, 10), http.StatusSeeOther)
}

type confirm struct{ Question, Detail, Action, Button, Back string }

func (s *site) confirmDeleteSource(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	at := "/sources/" + strconv.FormatInt(source.ID, 10)
	s.show(w, v, http.StatusOK, confirmView, pageData{Title: "Delete " + source.Title, Data: confirm{
		Question: "Delete " + source.Title + "?", Detail: "Its entries go too. A source someone reads can't be deleted.",
		Action: at + "/delete", Button: "Delete it", Back: at,
	}})
}

func (s *site) deleteSource(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	err := s.opts.App.DeleteSource(r.Context(), v.actor, source.ID)
	if errors.Is(err, app.ErrSourceInUse) {
		s.showSource(w, r, v, http.StatusConflict, "Someone still reads this, so it can't be deleted.", "")
		return
	}
	if err != nil {
		s.showSource(w, r, v, http.StatusServiceUnavailable, "That didn't work: "+err.Error(), "")
		return
	}
	s.audit(r.Context(), v.session.Name, "source.deleted", source.Slug, s.client(r))
	http.Redirect(w, r, "/sources", http.StatusSeeOther)
}

// paceForm is the pace form's fields, kept as typed so that a mistake can be fixed.
type paceForm struct {
	Preset, At, Days, TZ, RRule string
	Count                       int
	Speed                       float64
	MaxGap                      int
	bad                         string
}

// maxGapDays is the longest gap the replay form takes, a long way short of what
// would overflow a time.Duration.
const maxGapDays = 3650

// number reads a whole number that fits the database's int32 columns.
func number(raw string) (int, bool) {
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 32)
	return int(n), err == nil
}

func (s *site) defaultPace() paceForm {
	return paceForm{Preset: "daily", At: "07:00", TZ: s.opts.DefaultTimezone, Count: 1, Speed: 7, MaxGap: 3}
}

func paceFrom(r *http.Request) paceForm {
	f := r.PostForm
	p := paceForm{Preset: f.Get("preset"), At: strings.TrimSpace(f.Get("at")), Days: strings.TrimSpace(f.Get("days")),
		TZ: strings.TrimSpace(f.Get("tz")), RRule: strings.TrimSpace(f.Get("rrule"))}
	var ok bool
	if p.Count, ok = number(f.Get("count")); !ok && f.Get("count") != "" {
		p.bad = "How many at a time has to be a whole number."
	}
	if p.MaxGap, ok = number(f.Get("max_gap")); (!ok && f.Get("max_gap") != "") || p.MaxGap > maxGapDays {
		p.bad = "The most days between entries has to be a whole number, up to " + strconv.Itoa(maxGapDays) + "."
	}
	p.Speed, _ = strconv.ParseFloat(f.Get("speed"), 64)
	return p
}

func (p paceForm) pacing() (schedule.Pacing, error) {
	if p.bad != "" {
		return nil, errors.New(p.bad)
	}
	if p.Preset == "replay" {
		if p.Speed <= 0 {
			return nil, errors.New("the speed must be more than 0")
		}
		return schedule.ReplayPacing{Speed: p.Speed, MaxGap: time.Duration(max(p.MaxGap, 0)) * 24 * time.Hour}, nil
	}
	var rule string
	var err error
	switch p.Preset {
	case "daily":
		rule, err = pacing.Daily(p.At)
	case "weekdays":
		rule, err = pacing.Days(pacing.Weekdays, p.At)
	case "days":
		rule, err = pacing.Days(p.Days, p.At)
	case "rrule":
		rule = p.RRule
	default:
		err = errors.New("choose a pace")
	}
	if err != nil {
		return nil, err
	}
	return schedule.RRulePacing{RRule: rule, TimeZone: p.TZ, PerRelease: max(p.Count, 1)}, nil
}

type trackData struct {
	Source db.Source
	Name   string
	Pace   paceForm
}

func (s *site) showTrack(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	s.show(w, v, http.StatusOK, trackView, pageData{Title: "Track " + source.Title, Data: trackData{Source: source, Name: source.Title, Pace: s.defaultPace()}})
}

func (s *site) track(w http.ResponseWriter, r *http.Request, v visitor) {
	source, ok := s.sourceOf(w, r)
	if !ok {
		return
	}
	form := paceFrom(r)
	name := strings.TrimSpace(r.PostForm.Get("name"))
	if name == "" {
		name = source.Title
	}
	again := func(problem string) {
		s.show(w, v, http.StatusUnprocessableEntity, trackView, pageData{Title: "Track " + source.Title, Error: problem,
			Data: trackData{Source: source, Name: name, Pace: form}})
	}
	chosen, err := form.pacing()
	if err != nil {
		again(err.Error())
		return
	}
	ctx := r.Context()
	created, err := s.opts.App.CreateRead(ctx, reads.CreateParams{UserID: v.actor.UserID, SourceID: source.ID, Name: name, Pacing: chosen, Initial: 1})
	if err != nil {
		again(err.Error())
		return
	}
	if _, _, err := s.opts.Store.CreateFeedToken(ctx, v.actor, created.Read.ID, feed.RoleOwner, "me"); err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/reads/"+strconv.FormatInt(created.Read.ID, 10), http.StatusSeeOther)
}

type tokenRow struct {
	ID                int64
	Label, Page, Seen string
	Owner, Revoked    bool
}

type historyRow struct {
	When, What string
	From       int32
}

func (s *site) readOf(w http.ResponseWriter, r *http.Request, v visitor) (reads.View, bool) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return reads.View{}, false
	}
	found, err := s.opts.App.Reads.Get(r.Context(), v.actor, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return reads.View{}, false
	}
	if err != nil {
		s.fail(w, err)
		return reads.View{}, false
	}
	return found, true
}

func (s *site) read(w http.ResponseWriter, r *http.Request, v visitor) {
	s.showRead(w, r, v, http.StatusOK, "")
}

func (s *site) showRead(w http.ResponseWriter, r *http.Request, v visitor, status int, problem string) {
	found, ok := s.readOf(w, r, v)
	if !ok {
		return
	}
	ctx := r.Context()
	source, err := s.opts.Store.GetSource(ctx, found.Read.SourceID)
	if err != nil {
		s.fail(w, err)
		return
	}
	tokens, err := s.opts.Store.ListFeedTokens(ctx, v.actor, found.Read.ID, 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	segments, err := s.opts.App.Reads.Segments(ctx, v.actor, found.Read.ID, 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	row := rowOf(found, source.Title)
	data := struct {
		readRow
		Read    db.Read
		Source  db.Source
		Tokens  []tokenRow
		History []historyRow
	}{readRow: row, Read: found.Read, Source: source}
	for _, t := range tokens {
		token := tokenRow{ID: t.ID, Label: t.Label, Owner: t.Role == feed.RoleOwner, Revoked: t.RevokedAt != nil, Seen: stamp(t.LastSeenAt)}
		if !token.Revoked {
			raw, err := s.opts.Store.RawFeedToken(t)
			if err != nil {
				s.fail(w, err)
				return
			}
			if raw != "" {
				token.Page = strings.TrimRight(s.opts.BaseURL, "/") + "/f/" + raw
			}
		}
		data.Tokens = append(data.Tokens, token)
	}
	for _, seg := range segments {
		what := seg.Reason
		if p, err := reads.PacingOf(seg); err == nil && seg.Reason != string(schedule.ReasonSkip) && seg.Reason != string(schedule.ReasonJump) {
			what += ": " + schedule.Describe(p)
		}
		created := seg.CreatedAt
		data.History = append(data.History, historyRow{When: stamp(&created), What: what, From: seg.FromSeq})
	}
	s.show(w, v, status, readView, pageData{Title: found.Read.Name, Error: problem, Data: data})
}

func (s *site) adjust(w http.ResponseWriter, r *http.Request, v visitor, adjustment schedule.Adjustment) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, err := s.opts.App.Adjust(r.Context(), v.actor, id, adjustment)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.showRead(w, r, v, http.StatusConflict, "That didn't work: "+err.Error())
	default:
		http.Redirect(w, r, "/reads/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *site) pause(w http.ResponseWriter, r *http.Request, v visitor) {
	s.adjust(w, r, v, schedule.Pause{})
}

func (s *site) resume(w http.ResponseWriter, r *http.Request, v visitor) {
	s.adjust(w, r, v, schedule.Resume{})
}

func (s *site) skip(w http.ResponseWriter, r *http.Request, v visitor) {
	s.adjust(w, r, v, schedule.Skip{Count: 1})
}

func (s *site) jump(w http.ResponseWriter, r *http.Request, v visitor) {
	seq, ok := number(r.PostForm.Get("seq"))
	if !ok || seq < 0 {
		s.showRead(w, r, v, http.StatusUnprocessableEntity, "Give the number of an entry.")
		return
	}
	s.adjust(w, r, v, schedule.Jump{Seq: seq})
}

type repaceData struct {
	ID   int64
	Name string
	Pace paceForm
}

func (s *site) showRepace(w http.ResponseWriter, r *http.Request, v visitor) {
	found, ok := s.readOf(w, r, v)
	if !ok {
		return
	}
	s.show(w, v, http.StatusOK, repaceView, pageData{Title: "Change the pace", Data: repaceData{ID: found.Read.ID, Name: found.Read.Name, Pace: s.defaultPace()}})
}

func (s *site) repace(w http.ResponseWriter, r *http.Request, v visitor) {
	found, ok := s.readOf(w, r, v)
	if !ok {
		return
	}
	form := paceFrom(r)
	chosen, err := form.pacing()
	if err == nil {
		_, err = s.opts.App.Adjust(r.Context(), v.actor, found.Read.ID, schedule.Repace{Pacing: chosen})
	}
	if err != nil {
		s.show(w, v, http.StatusUnprocessableEntity, repaceView, pageData{Title: "Change the pace", Error: err.Error(),
			Data: repaceData{ID: found.Read.ID, Name: found.Read.Name, Pace: form}})
		return
	}
	http.Redirect(w, r, "/reads/"+strconv.FormatInt(found.Read.ID, 10), http.StatusSeeOther)
}

func (s *site) confirmDeleteRead(w http.ResponseWriter, r *http.Request, v visitor) {
	found, ok := s.readOf(w, r, v)
	if !ok {
		return
	}
	at := "/reads/" + strconv.FormatInt(found.Read.ID, 10)
	s.show(w, v, http.StatusOK, confirmView, pageData{Title: "Delete " + found.Read.Name, Data: confirm{
		Question: "Delete " + found.Read.Name + "?", Detail: "Its feed links stop working, for everyone you gave one to. To read the serial again, track it again.",
		Action: at + "/delete", Button: "Delete it", Back: at,
	}})
}

func (s *site) deleteRead(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.opts.App.DeleteRead(r.Context(), v.actor, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *site) addToken(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	label := strings.TrimSpace(r.PostForm.Get("label"))
	if label == "" {
		s.showRead(w, r, v, http.StatusUnprocessableEntity, "Say who the link is for.")
		return
	}
	role := feed.RoleViewer
	if r.PostForm.Get("owner") == "1" {
		role = feed.RoleOwner
	}
	_, _, err := s.opts.Store.CreateFeedToken(r.Context(), v.actor, id, role, label)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/reads/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *site) revokeToken(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.opts.Store.RevokeFeedToken(r.Context(), v.actor, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	back := "/"
	if read, err := strconv.ParseInt(r.PostForm.Get("read"), 10, 64); err == nil && read > 0 {
		back = "/reads/" + strconv.FormatInt(read, 10)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

type userRow struct {
	ID             int64
	Name, Role     string
	Disabled, Self bool
}

type inviteNote struct{ Name, Expires, URL string }

func (s *site) showUsers(w http.ResponseWriter, r *http.Request, v visitor, status int, problem string, invite *inviteNote) {
	users, err := s.opts.Store.ListUsers(r.Context(), 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := struct {
		Users  []userRow
		Invite *inviteNote
	}{Invite: invite}
	for _, u := range users {
		data.Users = append(data.Users, userRow{ID: u.ID, Name: u.Name, Role: u.Role, Disabled: u.DisabledAt != nil, Self: u.ID == v.actor.UserID})
	}
	s.show(w, v, status, usersView, pageData{Title: "Users", Error: problem, Data: data})
}

func (s *site) users(w http.ResponseWriter, r *http.Request, v visitor) {
	s.showUsers(w, r, v, http.StatusOK, "", nil)
}

func (s *site) invite(w http.ResponseWriter, r *http.Request, v visitor) {
	name := strings.ToLower(strings.TrimSpace(r.PostForm.Get("name")))
	role := store.RoleUser
	if r.PostForm.Get("admin") == "1" {
		role = store.RoleAdmin
	}
	reset := r.PostForm.Get("reset") == "1"
	invite, raw, err := s.opts.Store.CreateInvite(r.Context(), name, role, reset, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		s.showUsers(w, r, v, http.StatusConflict, "There's already a user called "+name+".", nil)
		return
	}
	if err != nil {
		s.showUsers(w, r, v, http.StatusUnprocessableEntity, "That didn't work: "+err.Error(), nil)
		return
	}
	s.audit(r.Context(), v.session.Name, "invite.created", invite.Name, s.client(r))
	expires := invite.ExpiresAt
	s.showUsers(w, r, v, http.StatusOK, "", &inviteNote{
		Name: invite.Name, Expires: stamp(&expires), URL: strings.TrimRight(s.opts.BaseURL, "/") + "/invite/" + raw,
	})
}

func userError(err error) string {
	switch {
	case errors.Is(err, store.ErrLastAdmin):
		return "That would leave no active admin."
	case errors.Is(err, store.ErrDeleteSelf):
		return "You can't delete yourself."
	}
	return "That didn't work: " + err.Error()
}

func (s *site) changeUser(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	var change store.UserChange
	yes, no := true, false
	action := r.PostForm.Get("action")
	switch action {
	case store.RoleAdmin, store.RoleUser:
		change.Role = &action
	case "disable":
		change.Disabled = &yes
	case "enable":
		change.Disabled = &no
	default:
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	user, err := s.opts.Store.UpdateUser(r.Context(), id, change, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.showUsers(w, r, v, http.StatusConflict, userError(err), nil)
		return
	}
	s.audit(r.Context(), v.session.Name, "user.changed", user.Name, s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

func (s *site) userOf(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return db.User{}, false
	}
	user, err := s.opts.Store.GetUser(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return db.User{}, false
	}
	if err != nil {
		s.fail(w, err)
		return db.User{}, false
	}
	return user, true
}

func (s *site) confirmDeleteUser(w http.ResponseWriter, r *http.Request, v visitor) {
	user, ok := s.userOf(w, r)
	if !ok {
		return
	}
	at := "/users/" + strconv.FormatInt(user.ID, 10)
	s.show(w, v, http.StatusOK, confirmView, pageData{Title: "Delete " + user.Name, Data: confirm{
		Question: "Delete " + user.Name + "?",
		Detail:   "Their reads go too, and every feed link they gave out stops working. It can't be undone, and a read can't be handed to someone else. To stop them logging in and keep their feeds, disable them instead.",
		Action:   at + "/delete", Button: "Delete them", Back: "/users",
	}})
}

func (s *site) deleteUser(w http.ResponseWriter, r *http.Request, v visitor) {
	user, ok := s.userOf(w, r)
	if !ok {
		return
	}
	if err := s.opts.Store.DeleteUser(r.Context(), v.actor, user.ID); err != nil {
		s.showUsers(w, r, v, http.StatusConflict, userError(err), nil)
		return
	}
	s.audit(r.Context(), v.session.Name, "user.deleted", user.Name, s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}

type sessionRow struct {
	ID                     int64
	IP, Agent, Since, Seen string
	Current                bool
}

type apiTokenRow struct {
	ID                int64
	Label, Made, Used string
	Revoked           bool
}

func (s *site) showAccount(w http.ResponseWriter, r *http.Request, v visitor, status int, problem, notice, newToken string) {
	ctx, now := r.Context(), s.opts.Now()
	sessions, err := s.opts.Store.ListSessions(ctx, v.actor, 0, maxReads, now)
	if err != nil {
		s.fail(w, err)
		return
	}
	tokens, err := s.opts.Store.ListAPITokens(ctx, v.actor, 0, maxReads)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := struct {
		Sessions []sessionRow
		Tokens   []apiTokenRow
		NewToken string
	}{NewToken: newToken}
	for _, sess := range sessions {
		since, seen := sess.CreatedAt, sess.LastSeenAt
		data.Sessions = append(data.Sessions, sessionRow{ID: sess.ID, IP: sess.Ip, Agent: sess.UserAgent, Since: stamp(&since), Seen: stamp(&seen), Current: sess.ID == v.session.ID})
	}
	for _, t := range tokens {
		made := t.CreatedAt
		data.Tokens = append(data.Tokens, apiTokenRow{ID: t.ID, Label: t.Label, Made: stamp(&made), Used: stamp(t.LastUsedAt), Revoked: t.RevokedAt != nil})
	}
	s.show(w, v, status, accountView, pageData{Title: "Your account", Error: problem, Notice: notice, Data: data})
}

func (s *site) account(w http.ResponseWriter, r *http.Request, v visitor) {
	s.showAccount(w, r, v, http.StatusOK, "", "", "")
}

func (s *site) changePassword(w http.ResponseWriter, r *http.Request, v visitor) {
	ctx := r.Context()
	_, _, ok, err := s.checkPassword(ctx, v.session.Name, r.PostForm.Get("current"))
	if err != nil {
		http.Error(w, "too busy; try again in a moment", http.StatusServiceUnavailable)
		return
	}
	if !ok {
		s.audit(ctx, v.session.Name, "password.change.failed", "", s.client(r))
		s.showAccount(w, r, v, http.StatusUnauthorized, "That isn't your current password.", "", "")
		return
	}
	chosen := r.PostForm.Get("password")
	if err := password.Check(chosen, v.session.Name); err != nil {
		s.showAccount(w, r, v, http.StatusUnprocessableEntity, err.Error(), "", "")
		return
	}
	hashCtx, cancel := context.WithTimeout(ctx, hashTimeout)
	defer cancel()
	hash, err := s.opts.Hasher.Hash(hashCtx, chosen)
	if err != nil {
		http.Error(w, "too busy; try again in a moment", http.StatusServiceUnavailable)
		return
	}
	if err := s.opts.Store.ChangePassword(ctx, v.actor.UserID, v.session.ID, hash, s.opts.Now()); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(ctx, v.session.Name, "password.changed", "", s.client(r))
	s.showAccount(w, r, v, http.StatusOK, "", "Changed. You've been logged out everywhere else.", "")
}

func (s *site) endSession(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.opts.Store.RevokeSession(r.Context(), v.actor, id, s.opts.Now())
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *site) addAPIToken(w http.ResponseWriter, r *http.Request, v visitor) {
	label := strings.TrimSpace(r.PostForm.Get("label"))
	if label == "" {
		s.showAccount(w, r, v, http.StatusUnprocessableEntity, "Give the token a label.", "", "")
		return
	}
	_, raw, err := s.opts.Store.CreateAPIToken(r.Context(), v.actor, label)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), v.session.Name, "api-token.created", label, s.client(r))
	s.showAccount(w, r, v, http.StatusOK, "", "", raw)
}

func (s *site) revokeAPIToken(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.opts.Store.RevokeAPIToken(r.Context(), v.actor, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, store.ErrLastToken):
		s.showAccount(w, r, v, http.StatusConflict, "That's your last API token. Make another before you revoke it.", "", "")
	case err != nil:
		s.fail(w, err)
	default:
		http.Redirect(w, r, "/account", http.StatusSeeOther)
	}
}
