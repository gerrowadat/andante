// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
)

func apiToken(t db.ApiToken) (client.APIToken, error) {
	return client.APIToken{
		ID:         t.ID,
		Label:      t.Label,
		CreatedAt:  t.CreatedAt,
		RevokedAt:  nullableValue(t.RevokedAt),
		LastUsedAt: nullableValue(t.LastUsedAt),
	}, nil
}

func nullableValue[T any](v *T) nullable.Nullable[T] {
	if v == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(*v)
}

func (h *handler) ListAPITokens(ctx context.Context, r gen.ListAPITokensRequestObject) (gen.ListAPITokensResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListAPITokens(ctx, actorOf(ctx), p.after, p.fetchLimit())
	if err != nil {
		return nil, err
	}
	key := func(t db.ApiToken) int64 { return t.ID }
	result, err := paginate(p, "/api-tokens", rows, key, apiToken)
	if err != nil {
		return nil, err
	}
	return gen.ListAPITokens200JSONResponse{
		Body:    client.APITokenList{Items: result.items, Next: result.next},
		Headers: gen.ListAPITokens200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) CreateAPIToken(ctx context.Context, r gen.CreateAPITokenRequestObject) (gen.CreateAPITokenResponseObject, error) {
	token, raw, err := h.store.CreateAPIToken(ctx, actorOf(ctx), r.Body.Label)
	if err != nil {
		return nil, err
	}
	return gen.CreateAPIToken201JSONResponse{
		ID:        token.ID,
		Label:     token.Label,
		CreatedAt: token.CreatedAt,
		Token:     raw,
	}, nil
}

func (h *handler) RevokeAPIToken(ctx context.Context, r gen.RevokeAPITokenRequestObject) (gen.RevokeAPITokenResponseObject, error) {
	err := h.store.RevokeAPIToken(ctx, actorOf(ctx), r.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, notFound("API token", r.ID)
	case errors.Is(err, store.ErrLastToken):
		return nil, newProblem(http.StatusConflict, problemConflict, "this is your last active API token")
	case err != nil:
		return nil, err
	}
	return gen.RevokeAPIToken204Response{}, nil
}
