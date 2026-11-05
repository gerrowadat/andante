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
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/db"
	"github.com/gerrowadat/andante/pkg/client"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/oapi-codegen/runtime/types"
)

const stateIndexing = "indexing"

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

// checkConfig validates a config through its strategy, and returns the strategy's
// name and the config to store.
func (h *handler) checkConfig(config client.SourceConfig) (string, []byte, error) {
	strategy, err := config.Discriminator()
	if err != nil {
		return "", nil, fmt.Errorf("read strategy: %w", err)
	}
	raw, err := config.MarshalJSON()
	if err != nil {
		return "", nil, fmt.Errorf("encode config: %w", err)
	}
	err = h.strategies.Validate(strategy, raw)
	switch {
	case errors.Is(err, discovery.ErrUnknownStrategy):
		return "", nil, newProblem(http.StatusUnprocessableEntity, problemInvalidConfig, "strategy "+strategy+" is not supported yet")
	case err != nil:
		return "", nil, newProblem(http.StatusUnprocessableEntity, problemInvalidConfig, err.Error())
	}
	return strategy, raw, nil
}

func (h *handler) ListSources(ctx context.Context, r gen.ListSourcesRequestObject) (gen.ListSourcesResponseObject, error) {
	p, err := parsePage(r.Params.Cursor, r.Params.Limit, 0)
	if err != nil {
		return nil, err
	}
	rows, err := h.store.ListSources(ctx, p.after, p.fetchLimit())
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

func (h *handler) CreateSource(ctx context.Context, r gen.CreateSourceRequestObject) (gen.CreateSourceResponseObject, error) {
	strategy, config, err := h.checkConfig(r.Body.Config)
	if err != nil {
		return nil, err
	}
	created, err := h.store.CreateSource(ctx, db.InsertSourceParams{
		Slug:        r.Body.Slug,
		Title:       r.Body.Title,
		HomeUrl:     r.Body.HomeURL,
		DefaultKind: string(r.Body.DefaultKind),
		ImageUrl:    r.Body.ImageURL,
		Strategy:    strategy,
		Config:      config,
		State:       stateIndexing,
	})
	if errors.Is(err, store.ErrConflict) {
		return nil, newProblem(http.StatusConflict, problemConflict, "slug "+r.Body.Slug+" is already in use")
	}
	if err != nil {
		return nil, err
	}
	body, err := source(created)
	if err != nil {
		return nil, err
	}
	return gen.CreateSource201JSONResponse(body), nil
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
	patch := r.Body
	updated, err := h.store.UpdateSource(ctx, r.ID, func(params *db.UpdateSourceParams) error {
		if patch.Title != nil {
			params.Title = *patch.Title
		}
		if patch.HomeURL != nil {
			params.HomeUrl = *patch.HomeURL
		}
		if patch.DefaultKind != nil {
			params.DefaultKind = string(*patch.DefaultKind)
		}
		if patch.ImageURL.IsNull() {
			params.ImageUrl = nil
		} else if patch.ImageURL.IsSpecified() {
			url := patch.ImageURL.MustGet()
			params.ImageUrl = &url
		}
		if patch.State != nil {
			params.State = string(*patch.State)
		}
		if patch.Config != nil {
			strategy, config, err := h.checkConfig(*patch.Config)
			if err != nil {
				return err
			}
			params.Strategy, params.Config = strategy, config
		}
		return nil
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	body, err := source(updated)
	if err != nil {
		return nil, err
	}
	return gen.UpdateSource200JSONResponse(body), nil
}

func (h *handler) DeleteSource(ctx context.Context, r gen.DeleteSourceRequestObject) (gen.DeleteSourceResponseObject, error) {
	err := h.store.DeleteSource(ctx, r.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, notFound("source", r.ID)
	case errors.Is(err, store.ErrConflict):
		return nil, newProblem(http.StatusConflict, problemConflict, "a read still uses this source")
	case err != nil:
		return nil, err
	}
	return gen.DeleteSource204Response{}, nil
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
