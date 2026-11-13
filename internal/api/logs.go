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
	items, more, err := h.app.CrawlLog(ctx, actorOf(ctx), r.ID, p.after, client.CrawlLogKindFetch, p.limit)
	if err != nil {
		return nil, logError(err)
	}
	fetches := make([]client.Fetch, 0, len(items))
	for _, item := range items {
		url, _ := item.URL.Get()
		fetches = append(fetches, client.Fetch{ID: item.ID, URL: url, Status: item.Status, FetchedAt: item.At, Error: item.Error})
	}
	list := client.FetchList{Items: fetches, Next: nullable.NewNullNullable[string]()}
	var link *string
	if more {
		list.Next, link = nextPage("/sources/"+strconv.FormatInt(r.ID, 10)+"/fetches", nil, fetches[len(fetches)-1].ID, p.limit)
	}
	return gen.ListFetches200JSONResponse{Body: list, Headers: gen.ListFetches200ResponseHeaders{Link: link}}, nil
}

func (h *handler) ListCrawlLog(ctx context.Context, r gen.ListCrawlLogRequestObject) (gen.ListCrawlLogResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	var (
		kind    client.CrawlLogKind
		filters url.Values
	)
	if r.Params.Kind != nil {
		kind = *r.Params.Kind
		filters = url.Values{"kind": {string(kind)}}
	}
	items, more, err := h.app.CrawlLog(ctx, actorOf(ctx), r.ID, p.after, kind, p.limit)
	if err != nil {
		return nil, logError(err)
	}
	list := client.CrawlLog{Items: items, Next: nullable.NewNullNullable[string]()}
	var link *string
	if more {
		list.Next, link = nextPage("/sources/"+strconv.FormatInt(r.ID, 10)+"/crawl-log", filters, items[len(items)-1].ID, p.limit)
	}
	return gen.ListCrawlLog200JSONResponse{Body: list, Headers: gen.ListCrawlLog200ResponseHeaders{Link: link}}, nil
}

func (h *handler) GetCrawledPage(ctx context.Context, r gen.GetCrawledPageRequestObject) (gen.GetCrawledPageResponseObject, error) {
	page, err := h.app.CrawledPage(ctx, actorOf(ctx), r.ID, r.Entry)
	if err != nil {
		return nil, logError(err)
	}
	return gen.GetCrawledPage200JSONResponse(page), nil
}
