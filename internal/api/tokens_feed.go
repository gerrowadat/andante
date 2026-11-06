// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/feed"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
)

const opmlPageSize = 100

func feedToken(t db.FeedToken) (client.FeedToken, error) {
	return client.FeedToken{
		ID:         t.ID,
		ReadID:     t.ReadID,
		Role:       client.Role(t.Role),
		Label:      t.Label,
		CreatedAt:  t.CreatedAt,
		RevokedAt:  nullableValue(t.RevokedAt),
		LastSeenAt: nullableValue(t.LastSeenAt),
	}, nil
}

func (h *handler) ListFeedTokens(ctx context.Context, r gen.ListFeedTokensRequestObject) (gen.ListFeedTokensResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListFeedTokens(ctx, r.ID, p.after, p.fetchLimit())
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("read", r.ID)
	}
	if err != nil {
		return nil, err
	}
	key := func(t db.FeedToken) int64 { return t.ID }
	result, err := paginate(p, fmt.Sprintf("/reads/%d/tokens", r.ID), rows, key, feedToken)
	if err != nil {
		return nil, err
	}
	return gen.ListFeedTokens200JSONResponse{
		Body:    client.FeedTokenList{Items: result.items, Next: result.next},
		Headers: gen.ListFeedTokens200ResponseHeaders{Link: result.link},
	}, nil
}

func (h *handler) CreateFeedToken(ctx context.Context, r gen.CreateFeedTokenRequestObject) (gen.CreateFeedTokenResponseObject, error) {
	read, err := h.store.GetRead(ctx, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("read", r.ID)
	}
	if err != nil {
		return nil, err
	}
	source, err := h.store.GetSource(ctx, read.SourceID)
	if err != nil {
		return nil, err
	}
	token, raw, err := h.store.CreateFeedToken(ctx, r.ID, string(r.Body.Role), r.Body.Label)
	if err != nil {
		return nil, err
	}
	issued := gen.CreateFeedToken201JSONResponse{
		ID:        token.ID,
		ReadID:    token.ReadID,
		Role:      client.Role(token.Role),
		Label:     token.Label,
		CreatedAt: token.CreatedAt,
		Token:     raw,
		Suggested: client.IssuedFeedTokenSuggested(feed.Suggested(source.DefaultKind)),
	}
	issued.Urls.Atom = feed.URL(h.baseURL, raw, "atom")
	issued.Urls.Rss = feed.URL(h.baseURL, raw, "rss")
	issued.Urls.JSON = feed.URL(h.baseURL, raw, "json")
	return issued, nil
}

func (h *handler) RevokeFeedToken(ctx context.Context, r gen.RevokeFeedTokenRequestObject) (gen.RevokeFeedTokenResponseObject, error) {
	err := h.store.RevokeFeedToken(ctx, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("feed token", r.ID)
	}
	if err != nil {
		return nil, err
	}
	return gen.RevokeFeedToken204Response{}, nil
}

// ExportOPML issues a new token per read. Raw tokens are never stored, so these are
// the only feed URLs the server can put in a file.
func (h *handler) ExportOPML(ctx context.Context, r gen.ExportOPMLRequestObject) (gen.ExportOPMLResponseObject, error) {
	var outlines []feed.Outline
	for after := int64(0); ; {
		page, err := h.store.ListReads(ctx, after, opmlPageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, read := range page {
			source, err := h.store.GetSource(ctx, read.SourceID)
			if err != nil {
				return nil, err
			}
			_, raw, err := h.store.CreateFeedToken(ctx, read.ID, string(r.Body.Issue), r.Body.Label)
			if err != nil {
				return nil, err
			}
			outlines = append(outlines, feed.Outline{
				Title:   source.Title + " — " + read.Name,
				FeedURL: feed.URL(h.baseURL, raw, feed.Suggested(source.DefaultKind)),
				HomeURL: source.HomeUrl,
			})
			after = read.ID
		}
	}
	body, err := feed.OPML(outlines)
	if err != nil {
		return nil, err
	}
	return gen.ExportOPML200TextXOpmlResponse{Body: bytes.NewReader(body), ContentLength: int64(len(body))}, nil
}
