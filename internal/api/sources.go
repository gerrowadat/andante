// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gerrowadat/andante/internal/api/gen"
	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/oapi-codegen/runtime/types"
)

func source(s db.Source) (client.Source, error) {
	var config client.SourceConfig
	if err := config.UnmarshalJSON(s.Config); err != nil {
		return client.Source{}, fmt.Errorf("decode config of source %d: %w", s.ID, err)
	}
	var extra map[string]any
	if err := json.Unmarshal(s.Extra, &extra); err != nil {
		return client.Source{}, fmt.Errorf("decode extra of source %d: %w", s.ID, err)
	}
	return client.Source{
		ID:            s.ID,
		Slug:          s.Slug,
		Title:         s.Title,
		HomeURL:       s.HomeUrl,
		DefaultKind:   client.Kind(s.DefaultKind),
		ImageURL:      nullableValue(s.ImageUrl),
		Extra:         extra,
		Config:        config,
		State:         client.SourceState(s.State),
		DueAt:         nullableValue(s.DueAt),
		LastCrawledAt: nullableValue(s.LastCrawledAt),
		LastError:     nullableValue(s.LastError),
		CreatedAt:     s.CreatedAt,
	}, nil
}

func (h *handler) ListSources(ctx context.Context, r gen.ListSourcesRequestObject) (gen.ListSourcesResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	filter := store.SourceFilter{}
	if r.Params.Q != nil {
		filter.Query = *r.Params.Q
	}
	if r.Params.Kind != nil {
		filter.Kind = string(*r.Params.Kind)
	}
	rows, err := h.store.ListSources(ctx, filter, p.after, p.fetchLimit())
	if err != nil {
		return nil, err
	}
	key := func(s db.Source) int64 { return s.ID }
	result, err := paginate(p, "/sources", rows, key, source)
	if err != nil {
		return nil, err
	}
	return gen.ListSources200JSONResponse{
		Body:    client.SourceList{Items: result.items, Next: result.next},
		Headers: gen.ListSources200ResponseHeaders{Link: result.link},
	}, nil
}

// sourceError turns what the app refuses into problem details.
func sourceError(err error) error {
	switch {
	case errors.Is(err, app.ErrAdminOnly):
		return forbidden("only an admin can change or delete a source")
	case errors.Is(err, app.ErrSourceInUse):
		return newProblem(http.StatusConflict, problemConflict, err.Error())
	}
	return err
}

func (h *handler) CreateSource(ctx context.Context, r gen.CreateSourceRequestObject) (gen.CreateSourceResponseObject, error) {
	created, err := h.app.CreateSource(ctx, replica.SourceCreate{SourceCreate: *r.Body})
	if err != nil {
		return nil, err
	}
	return gen.CreateSource201JSONResponse(created), nil
}

func (h *handler) GetSource(ctx context.Context, r gen.GetSourceRequestObject) (gen.GetSourceResponseObject, error) {
	found, err := h.store.GetSource(ctx, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	body, err := source(found)
	if err != nil {
		return nil, err
	}
	return gen.GetSource200JSONResponse(body), nil
}

func (h *handler) UpdateSource(ctx context.Context, r gen.UpdateSourceRequestObject) (gen.UpdateSourceResponseObject, error) {
	updated, err := h.app.UpdateSource(ctx, actorOf(ctx), r.ID, *r.Body)
	if err != nil {
		return nil, sourceError(err)
	}
	return gen.UpdateSource200JSONResponse(updated), nil
}

func (h *handler) DeleteSource(ctx context.Context, r gen.DeleteSourceRequestObject) (gen.DeleteSourceResponseObject, error) {
	if err := h.app.DeleteSource(ctx, actorOf(ctx), r.ID); err != nil {
		return nil, sourceError(err)
	}
	return gen.DeleteSource204Response{}, nil
}

func (h *handler) RequestCrawl(ctx context.Context, r gen.RequestCrawlRequestObject) (gen.RequestCrawlResponseObject, error) {
	if err := h.app.RequestCrawl(ctx, r.ID); err != nil {
		return nil, err
	}
	return gen.RequestCrawl202Response{}, nil
}

func (h *handler) CreateDiscovery(ctx context.Context, r gen.CreateDiscoveryRequestObject) (gen.CreateDiscoveryResponseObject, error) {
	result, err := h.catalogue.Discover(ctx, *r.Body)
	if err != nil {
		return nil, err
	}
	return gen.CreateDiscovery200JSONResponse(result), nil
}

func (h *handler) CreateProbe(ctx context.Context, r gen.CreateProbeRequestObject) (gen.CreateProbeResponseObject, error) {
	result, err := h.catalogue.Probe(ctx, *r.Body)
	if err != nil {
		return nil, err
	}
	return gen.CreateProbe200JSONResponse(result), nil
}

func entry(e db.Entry) (client.Entry, error) {
	var enclosures []client.Enclosure
	if err := json.Unmarshal(e.Enclosures, &enclosures); err != nil {
		return client.Entry{}, fmt.Errorf("decode enclosures of entry %d: %w", e.ID, err)
	}
	var extra map[string]any
	if err := json.Unmarshal(e.Extra, &extra); err != nil {
		return client.Entry{}, fmt.Errorf("decode extra of entry %d: %w", e.ID, err)
	}
	duration, err := isoDuration(e.Duration)
	if err != nil {
		return client.Entry{}, fmt.Errorf("convert duration of entry %d: %w", e.ID, err)
	}
	return client.Entry{
		ID:           e.ID,
		Seq:          e.Seq,
		Kind:         client.Kind(e.Kind),
		URL:          nullableValue(e.Url),
		GUID:         nullableValue(e.Guid),
		Title:        nullableValue(e.Title),
		Summary:      nullableValue(e.Summary),
		ContentHTML:  nullableValue(e.ContentHtml),
		Images:       e.Images,
		AltText:      nullableValue(e.AltText),
		Enclosures:   enclosures,
		Duration:     duration,
		Extra:        extra,
		OriginalDate: nullableValue(e.OriginalDate),
		DiscoveredAt: e.DiscoveredAt,
	}, nil
}

// isoDuration converts an interval. Entry durations come from feeds, so an
// interval holding months, which have no fixed length, does not occur.
func isoDuration(i pgtype.Interval) (nullable.Nullable[types.Duration], error) {
	if !i.Valid {
		return nullable.NewNullNullable[types.Duration](), nil
	}
	total := time.Duration(i.Microseconds)*time.Microsecond + time.Duration(i.Days)*24*time.Hour
	d, err := types.FromTimeDuration(total)
	if err != nil {
		return nil, err
	}
	return nullable.NewNullableWithValue(d), nil
}

func (h *handler) ListEntries(ctx context.Context, r gen.ListEntriesRequestObject) (gen.ListEntriesResponseObject, error) {
	// seq can be negative, so a list starts below any of them.
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, math.MinInt32)
	if err != nil {
		return nil, err
	}
	if p.after < math.MinInt32 || p.after > math.MaxInt32 {
		return nil, invalidRequest("cursor is not valid")
	}
	rows, err := h.store.ListEntries(ctx, r.ID, int32(p.after), p.fetchLimit())
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	key := func(e db.Entry) int64 { return int64(e.Seq) }
	path := "/sources/" + strconv.FormatInt(r.ID, 10) + "/entries"
	result, err := paginate(p, path, rows, key, entry)
	if err != nil {
		return nil, err
	}
	return gen.ListEntries200JSONResponse{
		Body:    client.EntryList{Items: result.items, Next: result.next},
		Headers: gen.ListEntries200ResponseHeaders{Link: result.link},
	}, nil
}
