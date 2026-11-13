// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"log/slog"

	"github.com/gerrowadat/andante/internal/catalogue/api/gen"
	"github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/logring"
)

func listLimit(limit *int32) int32 {
	if limit == nil {
		return defaultListLimit
	}
	return *limit
}

func (h *handler) ListLogs(_ context.Context, r gen.ListLogsRequestObject) (gen.ListLogsResponseObject, error) {
	filter := logring.Filter{Limit: int(listLimit(r.Params.Limit)), Level: slog.LevelDebug}
	if r.Params.Level != nil {
		filter.Level = logring.ParseLevel(string(*r.Params.Level))
	}
	if r.Params.Source != nil {
		filter.Attrs = map[string]string{"source": *r.Params.Source}
	}
	if r.Params.Before != nil {
		filter.Before = *r.Params.Before
	}
	records, more := h.logs.Records(filter)
	list := client.LogList{Items: make([]client.LogRecord, 0, len(records)), More: more}
	for _, rec := range records {
		list.Items = append(list.Items, client.LogRecord{
			ID: rec.ID, Time: rec.Time, Level: client.LogLevel(logring.LevelName(rec.Level)), Message: rec.Message, Attrs: rec.Attrs,
		})
	}
	return gen.ListLogs200JSONResponse(list), nil
}

func (h *handler) ListFetches(ctx context.Context, r gen.ListFetchesRequestObject) (gen.ListFetchesResponseObject, error) {
	if _, err := h.store.GetSource(ctx, r.ID); errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("source", r.ID)
	} else if err != nil {
		return nil, err
	}
	var before int64
	if r.Params.Before != nil {
		before = *r.Params.Before
	}
	limit := listLimit(r.Params.Limit)
	rows, err := h.store.ListFetches(ctx, r.ID, before, limit+1)
	if err != nil {
		return nil, err
	}
	list := client.FetchList{Items: make([]client.Fetch, 0, len(rows)), More: len(rows) > int(limit)}
	if list.More {
		rows = rows[:limit]
	}
	for _, row := range rows {
		list.Items = append(list.Items, client.Fetch{
			ID: row.ID, URL: row.Url, Status: nullableValue(row.Status), FetchedAt: row.FetchedAt, Error: nullableValue(row.Error),
		})
	}
	return gen.ListFetches200JSONResponse(list), nil
}
