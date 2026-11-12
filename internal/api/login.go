// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
)

// audit adds to the audit log. A failure is logged and doesn't undo what happened.
func (h *handler) audit(ctx context.Context, action, target string) {
	entry := store.AuditEntry{Who: actorOf(ctx).Name, Action: action, Target: target, IP: addressOf(ctx)}
	if err := h.store.Audit(ctx, entry, time.Now()); err != nil {
		h.logger.Error("could not write the audit log", "error", err)
	}
}

func (h *handler) CreateInvite(ctx context.Context, r gen.CreateInviteRequestObject) (gen.CreateInviteResponseObject, error) {
	req := store.InviteRequest{Role: store.RoleUser}
	if r.Body.Name != nil {
		req.Name = *r.Body.Name
	}
	if r.Body.Role != nil {
		req.Role = string(*r.Body.Role)
	}
	if r.Body.MaxUses != nil {
		req.MaxUses = *r.Body.MaxUses
	}
	if r.Body.ExpiresAt != nil {
		req.Expires = *r.Body.ExpiresAt
	}
	req.Reset = r.Body.Reset != nil && *r.Body.Reset
	invite, raw, err := h.store.CreateInvite(ctx, actorOf(ctx), req, time.Now())
	switch {
	case errors.Is(err, store.ErrNotAllowed):
		return nil, forbidden("you can't make that invite")
	case errors.Is(err, store.ErrInviteLimit):
		return nil, newProblem(http.StatusConflict, problemConflict, err.Error())
	case errors.Is(err, store.ErrBadInvite), errors.Is(err, store.ErrBadName):
		return nil, invalidRequest(err.Error())
	case errors.Is(err, store.ErrConflict):
		return nil, newProblem(http.StatusConflict, problemConflict, "there's already a user called "+req.Name)
	case errors.Is(err, store.ErrNotFound):
		return nil, newProblem(http.StatusConflict, problemConflict, "there's no user called "+req.Name+" to reset")
	case err != nil:
		return nil, err
	}
	h.audit(ctx, "invite.created", inviteTarget(invite))
	return gen.CreateInvite201JSONResponse{
		ID: invite.ID, Name: invite.Name, Role: client.InviteRole(invite.Role), MaxUses: invite.MaxUses,
		ExpiresAt: invite.ExpiresAt, URL: InviteURL(h.baseURL, raw),
	}, nil
}

// inviteTarget is what the audit log says an invite is for.
func inviteTarget(i store.Invite) string {
	if i.Name != "" {
		return i.Name
	}
	return fmt.Sprintf("invite %d", i.ID)
}

func inviteToAPI(i store.Invite) (client.InviteSummary, error) {
	out := client.InviteSummary{
		ID: i.ID, Name: i.Name, Role: client.InviteSummaryRole(i.Role), Reset: i.Reset, MaxUses: i.MaxUses, Uses: i.Uses,
		CreatedAt: i.CreatedAt, ExpiresAt: i.ExpiresAt,
	}
	if i.CreatedBy != "" {
		out.CreatedBy = &i.CreatedBy
	}
	return out, nil
}

func (h *handler) ListInvites(ctx context.Context, r gen.ListInvitesRequestObject) (gen.ListInvitesResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListInvites(ctx, actorOf(ctx), p.after, p.fetchLimit(), time.Now())
	if err != nil {
		return nil, err
	}
	key := func(i store.Invite) int64 { return i.ID }
	result, err := paginate(p, "/invites", rows, key, inviteToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListInvites200JSONResponse{
		Body:    client.InviteList{Items: result.items, Next: result.next},
		Headers: gen.ListInvites200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) DeleteInvite(ctx context.Context, r gen.DeleteInviteRequestObject) (gen.DeleteInviteResponseObject, error) {
	err := h.store.DeleteInvite(ctx, actorOf(ctx), r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("invite", r.ID)
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "invite.withdrawn", fmt.Sprintf("invite %d", r.ID))
	return gen.DeleteInvite204Response{}, nil
}

// InviteURL is where someone goes to use an invite.
func InviteURL(baseURL, raw string) string {
	return strings.TrimRight(baseURL, "/") + "/invite/" + raw
}

func sessionToAPI(s db.Session) (client.Session, error) {
	return client.Session{ID: s.ID, IP: s.Ip, UserAgent: s.UserAgent, CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt}, nil
}

func (h *handler) ListSessions(ctx context.Context, r gen.ListSessionsRequestObject) (gen.ListSessionsResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListSessions(ctx, actorOf(ctx), p.after, p.fetchLimit(), time.Now())
	if err != nil {
		return nil, err
	}
	key := func(s db.Session) int64 { return s.ID }
	result, err := paginate(p, "/sessions", rows, key, sessionToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListSessions200JSONResponse{
		Body:    client.SessionList{Items: result.items, Next: result.next},
		Headers: gen.ListSessions200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) RevokeSession(ctx context.Context, r gen.RevokeSessionRequestObject) (gen.RevokeSessionResponseObject, error) {
	err := h.store.RevokeSession(ctx, actorOf(ctx), r.ID, time.Now())
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("session", r.ID)
	}
	if err != nil {
		return nil, err
	}
	h.audit(ctx, "session.revoked", "")
	return gen.RevokeSession204Response{}, nil
}

func auditToAPI(line db.AuditLog) (client.AuditEntry, error) {
	return client.AuditEntry{ID: line.ID, At: line.At, Who: line.Who, Action: line.Action, Target: line.Target, IP: line.Ip}, nil
}

func (h *handler) ListAudit(ctx context.Context, r gen.ListAuditRequestObject) (gen.ListAuditResponseObject, error) {
	if !actorOf(ctx).Admin {
		return nil, forbidden("only an admin can read the audit log")
	}
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListAudit(ctx, p.after, p.fetchLimit())
	if err != nil {
		return nil, err
	}
	key := func(l db.AuditLog) int64 { return l.ID }
	result, err := paginate(p, "/audit", rows, key, auditToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListAudit200JSONResponse{
		Body:    client.AuditList{Items: result.items, Next: result.next},
		Headers: gen.ListAudit200ResponseHeaders{Link: result.link},
	}, nil
}
