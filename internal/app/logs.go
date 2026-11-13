// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package app

import (
	"context"
	"strconv"

	catalogue "github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/pkg/client"
)

// LogQuery picks log records. The zero value is the server's newest, at any level.
type LogQuery struct {
	// Catalogue reads the catalogue's log instead of the server's.
	Catalogue bool
	// Level is debug, info, warn or error. Empty is debug.
	Level string
	// Source is a source's slug, and Read a read's ID. Zero values match anything.
	Source string
	Read   int64
	// Before leaves out records from this ID on. Zero leaves nothing out.
	Before int64
	Limit  int32
}

// Logs returns what a service logged lately, newest first, and whether there's more
// past the limit. Only an admin can read them.
func (a *App) Logs(ctx context.Context, actor store.Actor, q LogQuery) ([]client.LogRecord, bool, error) {
	if !actor.Admin {
		return nil, false, ErrAdminOnly
	}
	if q.Catalogue {
		params := catalogue.ListLogsParams{Limit: &q.Limit}
		if q.Level != "" {
			level := catalogue.LogLevel(q.Level)
			params.Level = &level
		}
		if q.Source != "" {
			params.Source = &q.Source
		}
		if q.Before > 0 {
			params.Before = &q.Before
		}
		return a.Catalogue.Logs(ctx, params)
	}
	filter := logring.Filter{Level: logring.ParseLevel(q.Level), Before: q.Before, Limit: int(q.Limit), Attrs: map[string]string{}}
	if q.Source != "" {
		filter.Attrs["source"] = q.Source
	}
	if q.Read > 0 {
		filter.Attrs["read"] = strconv.FormatInt(q.Read, 10)
	}
	var records []logring.Record
	var more bool
	if a.LogRing != nil {
		records, more = a.LogRing.Records(filter)
	}
	out := make([]client.LogRecord, 0, len(records))
	for _, r := range records {
		out = append(out, client.LogRecord{
			ID: r.ID, Time: r.Time, Level: client.LogLevel(logring.LevelName(r.Level)), Message: r.Message, Attrs: r.Attrs,
		})
	}
	return out, more, nil
}

// CrawlLog returns the catalogue's crawl log for a source, newest first, and
// whether there's more past the limit. An empty kind means every kind. Only an
// admin can read it.
func (a *App) CrawlLog(ctx context.Context, actor store.Actor, sourceID, before int64, kind client.CrawlLogKind, limit int32) ([]client.CrawlLogItem, bool, error) {
	if !actor.Admin {
		return nil, false, ErrAdminOnly
	}
	params := catalogue.ListCrawlLogParams{Limit: &limit}
	if before > 0 {
		params.Before = &before
	}
	if kind != "" {
		k := catalogue.CrawlLogKind(kind)
		params.Kind = &k
	}
	return a.Catalogue.CrawlLog(ctx, sourceID, params)
}

// CrawledPage returns the page a crawl log event kept. Only an admin can read it.
func (a *App) CrawledPage(ctx context.Context, actor store.Actor, sourceID, entry int64) (client.CrawledPage, error) {
	if !actor.Admin {
		return client.CrawledPage{}, ErrAdminOnly
	}
	return a.Catalogue.CrawledPage(ctx, sourceID, entry)
}
