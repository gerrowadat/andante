// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/oapi-codegen/nullable"
)

func logError(err error) error {
	if errors.Is(err, app.ErrAdminOnly) {
		return forbidden("only an admin can read the logs")
	}
	return err
}

func (h *handler) ListLogs(ctx context.Context, r gen.ListLogsRequestObject) (gen.ListLogsResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	q := app.LogQuery{Before: p.after, Limit: p.limit}
	filters := url.Values{}
	if r.Params.Service != nil && *r.Params.Service == client.Catalogue {
		q.Catalogue = true
		filters.Set("service", string(*r.Params.Service))
	}
	if r.Params.Level != nil {
		q.Level = string(*r.Params.Level)
		filters.Set("level", q.Level)
	}
	if r.Params.Source != nil {
		q.Source = *r.Params.Source
		filters.Set("source", q.Source)
	}
	if r.Params.Read != nil {
		if q.Catalogue {
			return nil, invalidRequest("read filters the server's log, and the catalogue knows nothing about reads")
		}
		q.Read = *r.Params.Read
		filters.Set("read", strconv.FormatInt(q.Read, 10))
	}
	records, more, err := h.app.Logs(ctx, actorOf(ctx), q)
	if err != nil {
		return nil, logError(err)
	}
	list := client.LogList{Items: records, Next: nullable.NewNullNullable[string]()}
	var link *string
	if more {
		list.Next, link = nextPage("/logs", filters, records[len(records)-1].ID, p.limit)
	}
	return gen.ListLogs200JSONResponse{Body: list, Headers: gen.ListLogs200ResponseHeaders{Link: link}}, nil
}

func (h *handler) ListFetches(ctx context.Context, r gen.ListFetchesRequestObject) (gen.ListFetchesResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	fetches, more, err := h.app.Fetches(ctx, actorOf(ctx), r.ID, p.after, p.limit)
	if err != nil {
		return nil, logError(err)
	}
	list := client.FetchList{Items: fetches, Next: nullable.NewNullNullable[string]()}
	var link *string
	if more {
		list.Next, link = nextPage("/sources/"+strconv.FormatInt(r.ID, 10)+"/fetches", nil, fetches[len(fetches)-1].ID, p.limit)
	}
	return gen.ListFetches200JSONResponse{Body: list, Headers: gen.ListFetches200ResponseHeaders{Link: link}}, nil
}
