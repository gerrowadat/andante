// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package api is the catalogue's private API. It knows about sources and entries,
// and nothing about users or reads. Only andante-server calls it.
package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gerrowadat/andante/api"
	"github.com/gerrowadat/andante/internal/canon"
	"github.com/gerrowadat/andante/internal/catalogue/api/gen"
	"github.com/gerrowadat/andante/internal/catalogue/client"
	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/catalogue/store/db"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/find"
	"github.com/gerrowadat/andante/internal/httpapi"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/jackc/pgx/v5/pgtype"
	middleware "github.com/oapi-codegen/nethttp-middleware"
	"github.com/oapi-codegen/nullable"
	"github.com/oapi-codegen/runtime/types"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// BasePath is where the API is mounted.
const BasePath = "/v1"

const (
	stateIndexing     = "indexing"
	defaultProbeLimit = 10
	defaultSampleSize = 3

	defaultChangeLimit = 500
	defaultListLimit   = 100
	// changePoll is how often a held request looks for changes.
	changePoll = 200 * time.Millisecond
)

// Options are what the API needs.
type Options struct {
	Store        *store.Store
	Strategies   *discovery.Registry
	Prober       *discovery.Prober
	Finder       *find.Finder
	Token        string
	Registry     prometheus.Registerer
	Logger       *slog.Logger
	ProbeTimeout time.Duration
	// Stopping ends when the service starts to shut down, which lets go of held
	// /changes requests. A shutdown would otherwise wait for them.
	Stopping context.Context
	// Logs is what GET /logs reads. Nil keeps nothing.
	Logs *logring.Ring
}

type handler struct {
	store        *store.Store
	strategies   *discovery.Registry
	prober       *discovery.Prober
	finder       *find.Finder
	probeTimeout time.Duration
	stopping     context.Context //nolint:containedctx // It's the service's lifetime, not a request's.
	logs         *logring.Ring
}

// New returns the API. Mount it at BasePath.
func New(opts Options) (http.Handler, error) {
	spec, err := openapi3.NewLoader().LoadFromData(api.Catalogue)
	if err != nil {
		return nil, fmt.Errorf("load API description: %w", err)
	}
	if err := spec.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate API description: %w", err)
	}
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("route API description: %w", err)
	}

	if opts.Stopping == nil {
		opts.Stopping = context.Background()
	}
	h := &handler{
		store: opts.Store, strategies: opts.Strategies, prober: opts.Prober, finder: opts.Finder,
		probeTimeout: opts.ProbeTimeout, stopping: opts.Stopping, logs: opts.Logs,
	}
	if h.logs == nil {
		h.logs = logring.New(0, prometheus.NewRegistry())
	}
	kit := &httpapi.Kit{
		Logger: opts.Logger, Router: router,
		Valid: func(_ context.Context, token string) (bool, error) {
			return subtle.ConstantTimeCompare([]byte(token), []byte(opts.Token)) == 1, nil
		},
		SlowRoutes: []string{"POST " + BasePath + "/probes", "POST " + BasePath + "/discoveries"}, ProbeTimeout: opts.ProbeTimeout,
	}
	strict := gen.NewStrictHandlerWithOptions(h, nil, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpapi.WriteProblem(w, httpapi.RequestProblem(err))
		},
		ResponseErrorHandlerFunc: kit.WriteError,
	})
	routes := gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{
		BaseURL:     BasePath,
		BaseRouter:  http.NewServeMux(),
		Middlewares: []gen.MiddlewareFunc{kit.ExtendProbeDeadline, httpapi.RecordRoute},
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			httpapi.WriteProblem(w, httpapi.RequestProblem(err))
		},
	})
	validate := middleware.OapiRequestValidatorWithOptions(spec, &middleware.Options{
		Options:               openapi3filter.Options{AuthenticationFunc: kit.Authenticate},
		ErrorHandlerWithOpts:  kit.ValidationError,
		SilenceServersWarning: true,
	})
	requests := promauto.With(opts.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "andante_catalogue_requests_total",
		Help: "Catalogue API requests, by route pattern and status.",
	}, []string{"route", "status"})
	return httpapi.WithRequestID(httpapi.CountRequests(requests, httpapi.LimitBody(kit.RecoverPanics(validate(routes))))), nil
}

func nullableValue[T any](p *T) nullable.Nullable[T] {
	if p == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(*p)
}

func optionalValue(s string) nullable.Nullable[string] {
	if s == "" {
		return nullable.NewNullNullable[string]()
	}
	return nullable.NewNullableWithValue(s)
}

func nonNil[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}

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
		SourceID:     &e.SourceID,
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
		return "", nil, httpapi.NewProblem(http.StatusUnprocessableEntity, httpapi.TypeInvalidConfig, "strategy "+strategy+" is not supported yet")
	case err != nil:
		return "", nil, httpapi.NewProblem(http.StatusUnprocessableEntity, httpapi.TypeInvalidConfig, err.Error())
	}
	return strategy, raw, nil
}

// duplicate returns the problem for a config that another source already has, or nil.
func (h *handler) duplicate(ctx context.Context, strategy string, config []byte, except int64) error {
	key, err := canon.Key(strategy, config)
	if err != nil {
		return nil //nolint:nilerr // checkConfig has been through it, so this can't be the cause.
	}
	found, err := h.store.GetSourceByKey(ctx, key)
	if err != nil || found.ID == except {
		return nil //nolint:nilerr // No source means the conflict was something else.
	}
	problem := httpapi.NewProblem(http.StatusConflict, httpapi.TypeConflict, "source "+found.Slug+" already reads this")
	problem.Problem.ExistingSourceID = &found.ID
	return problem
}

func (h *handler) CreateSource(ctx context.Context, r gen.CreateSourceRequestObject) (gen.CreateSourceResponseObject, error) {
	strategy, config, err := h.checkConfig(r.Body.Config)
	if err != nil {
		return nil, err
	}
	if dup := h.duplicate(ctx, strategy, config, 0); dup != nil {
		return nil, dup
	}
	state := stateIndexing
	if r.Body.State != nil {
		state = string(*r.Body.State)
	}
	created, err := h.store.CreateSource(ctx, db.InsertSourceParams{
		Slug:        r.Body.Slug,
		Title:       r.Body.Title,
		HomeUrl:     r.Body.HomeURL,
		DefaultKind: string(r.Body.DefaultKind),
		ImageUrl:    r.Body.ImageURL,
		Strategy:    strategy,
		Config:      config,
		State:       state,
	})
	if errors.Is(err, store.ErrConflict) {
		if dup := h.duplicate(ctx, strategy, config, 0); dup != nil {
			return nil, dup
		}
		return nil, httpapi.NewProblem(http.StatusConflict, httpapi.TypeConflict, "slug "+r.Body.Slug+" is already in use")
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
		return nil, httpapi.NotFound("source", r.ID)
	}
	if errors.Is(err, store.ErrConflict) && patch.Config != nil {
		if strategy, config, checked := h.checkConfig(*patch.Config); checked == nil {
			if dup := h.duplicate(ctx, strategy, config, r.ID); dup != nil {
				return nil, dup
			}
		}
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
		return nil, httpapi.NotFound("source", r.ID)
	case errors.Is(err, store.ErrConflict):
		return nil, httpapi.NewProblem(http.StatusConflict, httpapi.TypeConflict, "a read still uses this source")
	case err != nil:
		return nil, err
	}
	return gen.DeleteSource204Response{}, nil
}

func (h *handler) RequestCrawl(ctx context.Context, r gen.RequestCrawlRequestObject) (gen.RequestCrawlResponseObject, error) {
	err := h.store.RequestCrawl(ctx, r.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	return gen.RequestCrawl202Response{}, nil
}

func (h *handler) SetDemand(ctx context.Context, r gen.SetDemandRequestObject) (gen.SetDemandResponseObject, error) {
	err := h.store.SetDemand(ctx, r.ID, &r.Body.Seq)
	if errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	return gen.SetDemand204Response{}, nil
}

func (h *handler) ClearDemand(ctx context.Context, r gen.ClearDemandRequestObject) (gen.ClearDemandResponseObject, error) {
	err := h.store.SetDemand(ctx, r.ID, nil)
	if errors.Is(err, store.ErrNotFound) {
		return nil, httpapi.NotFound("source", r.ID)
	}
	if err != nil {
		return nil, err
	}
	return gen.ClearDemand204Response{}, nil
}

func (h *handler) CreateProbe(ctx context.Context, r gen.CreateProbeRequestObject) (gen.CreateProbeResponseObject, error) {
	strategy, config, err := h.checkConfig(r.Body.Config)
	if err != nil {
		return nil, err
	}
	req := discovery.ProbeRequest{Strategy: strategy, Config: config, Kind: string(client.Page), Limit: defaultProbeLimit}
	if r.Body.DefaultKind != nil {
		req.Kind = string(*r.Body.DefaultKind)
	}
	if r.Body.Limit != nil {
		req.Limit = int(*r.Body.Limit)
	}

	ctx, cancel := context.WithTimeout(ctx, h.probeTimeout)
	defer cancel()
	found, err := h.prober.Probe(ctx, req)
	if err != nil {
		return nil, httpapi.NewProblem(http.StatusUnprocessableEntity, httpapi.TypeProbeFailed, err.Error())
	}

	return gen.CreateProbe200JSONResponse{
		Entries:    sample(found.Entries),
		Warnings:   nonNil(found.Warnings),
		DatedShare: found.DatedShare,
	}, nil
}

// sample turns entries that were found and not saved into the API's shape.
func sample(found []discovery.Entry) []client.Entry {
	now := time.Now().UTC()
	entries := make([]client.Entry, 0, len(found))
	for i, e := range found {
		entries = append(entries, client.Entry{
			Seq:          int32(i), //nolint:gosec // A probe is 25 entries at most.
			Kind:         client.Kind(e.Kind),
			URL:          optionalValue(e.URL),
			GUID:         nullable.NewNullNullable[string](),
			Title:        optionalValue(e.Title),
			Summary:      optionalValue(e.Summary),
			ContentHTML:  nullable.NewNullNullable[string](),
			Images:       nonNil(e.Images),
			AltText:      optionalValue(e.AltText),
			Enclosures:   []client.Enclosure{},
			Duration:     nullable.NewNullNullable[types.Duration](),
			Extra:        map[string]any{},
			OriginalDate: nullableValue(e.Date),
			DiscoveredAt: now,
		})
	}
	return entries
}

func (h *handler) CreateDiscovery(ctx context.Context, r gen.CreateDiscoveryRequestObject) (gen.CreateDiscoveryResponseObject, error) {
	address, err := url.Parse(r.Body.URL)
	if err != nil || (address.Scheme != "http" && address.Scheme != "https") || address.Host == "" {
		return nil, httpapi.NewProblem(http.StatusUnprocessableEntity, httpapi.TypeInvalidConfig, "url must be an absolute http(s) URL")
	}
	limit := defaultSampleSize
	if r.Body.Limit != nil {
		limit = int(*r.Body.Limit)
	}

	ctx, cancel := context.WithTimeout(ctx, h.probeTimeout)
	defer cancel()
	found, err := h.finder.Find(ctx, r.Body.URL, limit)
	if err != nil {
		return nil, httpapi.NewProblem(http.StatusUnprocessableEntity, httpapi.TypeProbeFailed, err.Error())
	}
	candidates := make([]client.Candidate, 0, len(found))
	for _, c := range found {
		var config client.SourceConfig
		if err := config.UnmarshalJSON(c.Config); err != nil {
			return nil, fmt.Errorf("decode config of a candidate: %w", err)
		}
		candidates = append(candidates, client.Candidate{
			Title: c.Title, DefaultKind: client.Kind(c.Kind), HomeURL: c.HomeURL, Config: config,
			Sample: sample(c.Sample), DatedShare: c.DatedShare, Warnings: nonNil(c.Warnings),
			ExistingSourceID: nullableValue(c.ExistingSourceID), SimilarSourceIds: nonNil(c.SimilarSourceIDs),
		})
	}
	return gen.CreateDiscovery200JSONResponse{Candidates: candidates}, nil
}

func (h *handler) changes(ctx context.Context, after int64, limit int32) ([]client.Change, error) {
	found, err := h.store.ChangesAfter(ctx, after, limit)
	if err != nil {
		return nil, err
	}
	changes := make([]client.Change, 0, len(found))
	for _, c := range found {
		change := client.Change{Revision: c.Revision, DeletedSourceID: c.DeletedSource}
		if c.Source != nil {
			s, err := source(*c.Source)
			if err != nil {
				return nil, err
			}
			change.Source = &s
		}
		if c.Entry != nil {
			e, err := entry(*c.Entry)
			if err != nil {
				return nil, err
			}
			change.Entry = &e
		}
		changes = append(changes, change)
	}
	return changes, nil
}

func (h *handler) ListChanges(ctx context.Context, r gen.ListChangesRequestObject) (gen.ListChangesResponseObject, error) {
	var after int64
	if r.Params.After != nil {
		after = *r.Params.After
	}
	limit := int32(defaultChangeLimit)
	if r.Params.Limit != nil {
		limit = *r.Params.Limit
	}
	var wait time.Duration
	if r.Params.Wait != nil {
		wait = time.Duration(*r.Params.Wait) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	for {
		// The wait's context is only for waiting, so a query can't be cut off by it.
		changes, err := h.changes(context.WithoutCancel(ctx), after, limit)
		if err != nil {
			return nil, err
		}
		if len(changes) > 0 {
			return gen.ListChanges200JSONResponse{Changes: changes}, nil
		}
		select {
		case <-ctx.Done():
			return gen.ListChanges200JSONResponse{Changes: []client.Change{}}, nil
		case <-h.stopping.Done():
			return gen.ListChanges200JSONResponse{Changes: []client.Change{}}, nil
		case <-time.After(changePoll):
		}
	}
}
