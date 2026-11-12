// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"math"
	"net/http"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/bootstrap"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/pkg/client"
)

// Bootstrap makes the first admin. It needs no API token: the bootstrap token is the
// authentication. Whenever there's nothing to set up it's a 404, as on the website.
func (h *handler) Bootstrap(ctx context.Context, r gen.BootstrapRequestObject) (gen.BootstrapResponseObject, error) {
	closed := newProblem(http.StatusNotFound, httpapi.TypeBlank, "there's nothing to set up")
	if h.bootstrap == nil {
		return nil, closed
	}
	admin, err := h.bootstrap.Make(ctx, r.Body.Token, r.Body.Name, addressOf(ctx))
	var throttled *bootstrap.ThrottledError
	switch {
	case errors.As(err, &throttled):
		wait := int(math.Ceil(throttled.Wait.Seconds()))
		detail := "too many wrong tokens from this address; wait and try again"
		return gen.Bootstrap429ApplicationProblemPlusJSONResponse{
			Body: client.Problem{
				Type: httpapi.TypeBlank, Title: http.StatusText(http.StatusTooManyRequests),
				Status: http.StatusTooManyRequests, Detail: &detail,
			},
			Headers: gen.Bootstrap429ResponseHeaders{RetryAfter: &wait},
		}, nil
	case errors.Is(err, bootstrap.ErrClosed):
		return nil, closed
	case errors.Is(err, bootstrap.ErrWrongToken):
		return nil, newProblem(http.StatusUnauthorized, httpapi.TypeUnauthorized, "that isn't the bootstrap token")
	case errors.Is(err, store.ErrConflict):
		return nil, newProblem(http.StatusConflict, problemConflict, "there's already a user called "+r.Body.Name)
	case err != nil:
		return nil, err
	}
	user, err := userToAPI(admin.User)
	if err != nil {
		return nil, err
	}
	result := gen.Bootstrap201JSONResponse{User: user, Token: admin.APIToken}
	if admin.Invite != "" {
		link := InviteURL(h.baseURL, admin.Invite)
		result.PasswordURL = &link
	}
	return result, nil
}
