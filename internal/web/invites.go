// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/store"
)

// invitesPage is how many invites the page lists at once.
const invitesPage = 100

type inviteRow struct {
	ID                  int64
	Name, Role, Expires string
	CreatedBy           string
	Reset               bool
	MaxUses, Uses       int32
}

type madeInvite struct {
	Name, Expires, URL string
	Uses               int32
}

func (s *site) showInvites(w http.ResponseWriter, r *http.Request, v visitor, status int, problem string, made *madeInvite) {
	// ?after= pages through them, since an admin sees everyone's.
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	invites, err := s.opts.Store.ListInvites(r.Context(), v.actor, after, invitesPage, s.opts.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	settings, err := s.opts.Store.GetInviteSettings(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	out, err := s.opts.Store.InvitePlaces(r.Context(), v.actor, s.opts.Now())
	if err != nil {
		s.fail(w, err)
		return
	}
	data := struct {
		Invites     []inviteRow
		Made        *madeInvite
		Limit, Left int32
		More        int64
	}{Made: made, Limit: settings.Limit, Left: max(settings.Limit-out, 0)}
	for _, i := range invites {
		expires := i.ExpiresAt
		data.Invites = append(data.Invites, inviteRow{
			ID: i.ID, Name: i.Name, Role: i.Role, Expires: stamp(&expires), CreatedBy: i.CreatedBy, Reset: i.Reset,
			MaxUses: i.MaxUses, Uses: i.Uses,
		})
	}
	if len(invites) == invitesPage {
		data.More = invites[len(invites)-1].ID
	}
	s.show(w, v, status, invitesView, pageData{Title: "Invites", Error: problem, Data: data})
}

func (s *site) invites(w http.ResponseWriter, r *http.Request, v visitor) {
	s.showInvites(w, r, v, http.StatusOK, "", nil)
}

func (s *site) createInvite(w http.ResponseWriter, r *http.Request, v visitor) {
	req := store.InviteRequest{Name: strings.ToLower(strings.TrimSpace(r.PostForm.Get("name"))), Role: store.RoleUser}
	if r.PostForm.Get("admin") == "1" {
		req.Role = store.RoleAdmin
	}
	if uses, err := strconv.ParseInt(r.PostForm.Get("uses"), 10, 32); err == nil {
		req.MaxUses = int32(uses)
	}
	if days, err := strconv.Atoi(r.PostForm.Get("days")); err == nil {
		req.Expires = s.opts.Now().Add(time.Duration(days) * 24 * time.Hour)
	}
	invite, raw, err := s.opts.Store.CreateInvite(r.Context(), v.actor, req, s.opts.Now())
	switch {
	case errors.Is(err, store.ErrNotAllowed):
		http.Error(w, "you can't make that invite", http.StatusForbidden)
		return
	case errors.Is(err, store.ErrInviteLimit):
		s.showInvites(w, r, v, http.StatusConflict, "That's more people than you have places left for.", nil)
		return
	case errors.Is(err, store.ErrConflict):
		s.showInvites(w, r, v, http.StatusConflict, "There's already a user called "+req.Name+".", nil)
		return
	case errors.Is(err, store.ErrBadName):
		s.showInvites(w, r, v, http.StatusUnprocessableEntity, "A name is 1 to 64 lower-case letters, digits, dots, dashes or underscores.", nil)
		return
	case errors.Is(err, store.ErrBadInvite):
		s.showInvites(w, r, v, http.StatusUnprocessableEntity, "A link is for 1 to 100 people, for 1 to 30 days, and a link with a name is for one person.", nil)
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	target := invite.Name
	if target == "" {
		target = "invite " + strconv.FormatInt(invite.ID, 10)
	}
	s.audit(r.Context(), v.session.Name, "invite.created", target, s.client(r))
	expires := invite.ExpiresAt
	s.showInvites(w, r, v, http.StatusOK, "", &madeInvite{
		Name: invite.Name, Uses: invite.MaxUses, Expires: stamp(&expires), URL: strings.TrimRight(s.opts.BaseURL, "/") + "/invite/" + raw,
	})
}

// withdrawInvite works whether or not they can still make invites, so that someone
// can always take back a link of their own.
func (s *site) withdrawInvite(w http.ResponseWriter, r *http.Request, v visitor) {
	id, ok := idOf(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := s.opts.Store.DeleteInvite(r.Context(), v.actor, id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), v.session.Name, "invite.withdrawn", "invite "+strconv.FormatInt(id, 10), s.client(r))
	http.Redirect(w, r, "/invites", http.StatusSeeOther)
}

func (s *site) inviteSettings(w http.ResponseWriter, r *http.Request, v visitor) {
	who := store.InvitesByAdmins
	if r.PostForm.Get("users") == "1" {
		who = store.InvitesByUsers
	}
	limit, err := strconv.ParseInt(r.PostForm.Get("limit"), 10, 32)
	if err != nil || limit < 0 {
		s.showUsers(w, r, v, http.StatusUnprocessableEntity, "The number of places is a whole number, 0 or more.", nil)
		return
	}
	l := int32(limit)
	if err := s.opts.Store.SetInviteSettings(r.Context(), &who, &l); err != nil {
		s.fail(w, err)
		return
	}
	s.audit(r.Context(), v.session.Name, "settings.changed", "invites."+who, s.client(r))
	s.audit(r.Context(), v.session.Name, "settings.changed", "invite-limit."+strconv.FormatInt(limit, 10), s.client(r))
	http.Redirect(w, r, "/users", http.StatusSeeOther)
}
