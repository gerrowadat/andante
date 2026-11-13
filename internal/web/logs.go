// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/pkg/client"
)

const (
	logPage       = 100
	crawlLogShown = 50
	// pageShown is as much of a kept page as the source page shows. The API
	// has all of it.
	pageShown = 256 * 1024
)

var logLevels = []string{"debug", "info", "warn", "error"}

type logRow struct {
	Time, Level, Message, Attrs string
}

func logRows(records []client.LogRecord) []logRow {
	rows := make([]logRow, 0, len(records))
	for _, r := range records {
		keys := make([]string, 0, len(r.Attrs))
		for k := range r.Attrs {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		attrs := make([]string, 0, len(keys))
		for _, k := range keys {
			attrs = append(attrs, k+"="+r.Attrs[k])
		}
		rows = append(rows, logRow{
			Time: r.Time.UTC().Format(time.DateTime), Level: string(r.Level), Message: r.Message, Attrs: strings.Join(attrs, " "),
		})
	}
	return rows
}

// logs shows what the server or the catalogue logged lately, newest first.
func (s *site) logs(w http.ResponseWriter, r *http.Request, v visitor) {
	form := r.URL.Query()
	q := app.LogQuery{
		Catalogue: form.Get("service") == "catalogue",
		Level:     form.Get("level"),
		Source:    strings.TrimSpace(form.Get("source")),
		Limit:     logPage,
	}
	if !slices.Contains(logLevels, q.Level) {
		q.Level = "debug"
	}
	// A number that doesn't parse is no filter, which is what an empty box means too.
	q.Read, _ = strconv.ParseInt(form.Get("read"), 10, 64)
	q.Before, _ = strconv.ParseInt(form.Get("before"), 10, 64)
	if q.Catalogue {
		q.Read = 0
	}

	data := struct {
		app.LogQuery
		Levels []string
		Rows   []logRow
		Older  string
	}{LogQuery: q, Levels: logLevels}
	var problem string
	records, more, err := s.opts.App.Logs(r.Context(), v.actor, q)
	if err != nil {
		// Most likely the catalogue is down, and the page says so rather than failing.
		s.opts.Logger.Warn("could not read the logs", "catalogue", q.Catalogue, "error", err)
		problem = "The catalogue didn't answer."
	}
	data.Rows = logRows(records)
	if more {
		older := url.Values{"level": {q.Level}, "before": {strconv.FormatInt(records[len(records)-1].ID, 10)}}
		if q.Catalogue {
			older.Set("service", "catalogue")
		}
		if q.Source != "" {
			older.Set("source", q.Source)
		}
		if q.Read > 0 {
			older.Set("read", strconv.FormatInt(q.Read, 10))
		}
		data.Older = "?" + older.Encode()
	}
	s.show(w, v, http.StatusOK, logsView, pageData{Title: "Logs", Error: problem, Data: data})
}

type crawlRow struct {
	At, URL, Status, Error, Level, Message string
	Event                                  bool
	// Page is the page the crawl gave up on, for the one event that kept it,
	// and Cut says it was too long to show all of.
	Page string
	Cut  bool
}

// crawlLog is a source's latest crawl log, for an admin. ok is false when the
// catalogue couldn't say.
func (s *site) crawlLog(r *http.Request, v visitor, sourceID int64) ([]crawlRow, bool) {
	items, _, err := s.opts.App.CrawlLog(r.Context(), v.actor, sourceID, 0, "", crawlLogShown)
	if err != nil {
		s.opts.Logger.Warn("could not read the crawl log", "source_id", sourceID, "error", err)
		return nil, false
	}
	rows := make([]crawlRow, 0, len(items))
	for _, item := range items {
		row := crawlRow{At: stamp(&item.At), Event: item.Kind == client.CrawlLogKindEvent}
		row.URL, _ = item.URL.Get()
		row.Error, _ = item.Error.Get()
		row.Message, _ = item.Message.Get()
		if status, err := item.Status.Get(); err == nil {
			row.Status = strconv.Itoa(int(status))
		}
		if item.Level != nil {
			row.Level = string(*item.Level)
		}
		if item.HasPage {
			page, err := s.opts.App.CrawledPage(r.Context(), v.actor, sourceID, item.ID)
			if err != nil {
				s.opts.Logger.Warn("could not read a crawled page", "source_id", sourceID, "entry", item.ID, "error", err)
			} else {
				row.Page, row.Cut = page.Body, len(page.Body) > pageShown
				if row.Cut {
					row.Page = strings.ToValidUTF8(page.Body[:pageShown], "")
				}
			}
		}
		rows = append(rows, row)
	}
	return rows, true
}
