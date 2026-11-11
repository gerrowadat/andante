// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
)

func userToAPI(u db.User) (client.User, error) {
	return client.User{
		ID: u.ID, Name: u.Name, Role: client.UserRole(u.Role), CreatedAt: u.CreatedAt, DisabledAt: nullableValue(u.DisabledAt),
	}, nil
}

func userError(id int64, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return notFound("user", id)
	case errors.Is(err, store.ErrLastAdmin):
		return newProblem(http.StatusConflict, problemConflict, "that would leave no active admin")
	case errors.Is(err, store.ErrDeleteSelf):
		return newProblem(http.StatusConflict, problemConflict, "you can't delete yourself")
	}
	return err
}

func (h *handler) ListUsers(ctx context.Context, r gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	if !actorOf(ctx).Admin {
		return nil, forbidden("only an admin can list users")
	}
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListUsers(ctx, p.after, p.fetchLimit())
	if err != nil {
		return nil, err
	}
	key := func(u db.User) int64 { return u.ID }
	result, err := paginate(p, "/users", rows, key, userToAPI)
	if err != nil {
		return nil, err
	}
	return gen.ListUsers200JSONResponse{
		Body:    client.UserList{Items: result.items, Next: result.next},
		Headers: gen.ListUsers200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) UpdateUser(ctx context.Context, r gen.UpdateUserRequestObject) (gen.UpdateUserResponseObject, error) {
	if !actorOf(ctx).Admin {
		return nil, forbidden("only an admin can change a user")
	}
	change := store.UserChange{Disabled: r.Body.Disabled}
	if r.Body.Role != nil {
		role := string(*r.Body.Role)
		change.Role = &role
	}
	updated, err := h.store.UpdateUser(ctx, r.ID, change, time.Now())
	if err != nil {
		return nil, userError(r.ID, err)
	}
	h.audit(ctx, "user.changed", updated.Name)
	body, err := userToAPI(updated)
	if err != nil {
		return nil, err
	}
	return gen.UpdateUser200JSONResponse(body), nil
}

func (h *handler) DeleteUser(ctx context.Context, r gen.DeleteUserRequestObject) (gen.DeleteUserResponseObject, error) {
	actor := actorOf(ctx)
	if !actor.Admin {
		return nil, forbidden("only an admin can delete a user")
	}
	user, err := h.store.GetUser(ctx, r.ID)
	if err != nil {
		return nil, userError(r.ID, err)
	}
	if err := h.store.DeleteUser(ctx, actor, r.ID); err != nil {
		return nil, userError(r.ID, err)
	}
	h.audit(ctx, "user.deleted", user.Name)
	return gen.DeleteUser204Response{}, nil
}

func (h *handler) settings(ctx context.Context) (client.Settings, error) {
	on, err := h.store.RequireTOTP(ctx)
	if err != nil {
		return client.Settings{}, err
	}
	return client.Settings{RequireTwoFactor: on}, nil
}

func (h *handler) GetSettings(ctx context.Context, _ gen.GetSettingsRequestObject) (gen.GetSettingsResponseObject, error) {
	if !actorOf(ctx).Admin {
		return nil, forbidden("only an admin can read the settings")
	}
	body, err := h.settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.GetSettings200JSONResponse(body), nil
}

func (h *handler) UpdateSettings(ctx context.Context, r gen.UpdateSettingsRequestObject) (gen.UpdateSettingsResponseObject, error) {
	if !actorOf(ctx).Admin {
		return nil, forbidden("only an admin can change the settings")
	}
	if on := r.Body.RequireTwoFactor; on != nil {
		if err := h.store.SetRequireTOTP(ctx, *on); err != nil {
			return nil, err
		}
		what := "two-factor.optional"
		if *on {
			what = "two-factor.required"
		}
		h.audit(ctx, "settings.changed", what)
	}
	body, err := h.settings(ctx)
	if err != nil {
		return nil, err
	}
	return gen.UpdateSettings200JSONResponse(body), nil
}
