// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"errors"
	"log/slog"
	"strings"

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

func (h *handler) ListCrawlLog(ctx context.Context, r gen.ListCrawlLogRequestObject) (gen.ListCrawlLogResponseObject, error) {
	if _, err := h.store.GetSource(ctx, r.ID); errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("source", r.ID)
	} else if err != nil {
		return nil, err
	}
	var (
		before int64
		kind   string
	)
	if r.Params.Before != nil {
		before = *r.Params.Before
	}
	if r.Params.Kind != nil {
		kind = string(*r.Params.Kind)
	}
	limit := listLimit(r.Params.Limit)
	rows, err := h.store.ListCrawlLog(ctx, r.ID, before, kind, limit+1)
	if err != nil {
		return nil, err
	}
	list := client.CrawlLog{Items: make([]client.CrawlLogItem, 0, len(rows)), More: len(rows) > int(limit)}
	if list.More {
		rows = rows[:limit]
	}
	for _, row := range rows {
		item := client.CrawlLogItem{
			ID: row.ID, Kind: client.CrawlLogKind(row.Kind), At: row.FetchedAt, URL: nullableValue(row.Url),
			Status: nullableValue(row.Status), Error: nullableValue(row.Error), Message: nullableValue(row.Message), HasPage: row.HasPage,
		}
		if row.Level != nil {
			level := client.LogLevel(*row.Level)
			item.Level = &level
		}
		list.Items = append(list.Items, item)
	}
	return gen.ListCrawlLog200JSONResponse(list), nil
}

func (h *handler) GetCrawledPage(ctx context.Context, r gen.GetCrawledPageRequestObject) (gen.GetCrawledPageResponseObject, error) {
	page, err := h.store.CrawledPage(ctx, r.ID, r.Entry)
	if errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("crawled page", r.Entry)
	}
	if err != nil {
		return nil, err
	}
	var url string
	if page.Url != nil {
		url = *page.Url
	}
	return gen.GetCrawledPage200JSONResponse(client.CrawledPage{
		URL: url, FetchedAt: page.FetchedAt, Body: strings.ToValidUTF8(string(page.Body), "\uFFFD"),
	}), nil
}
