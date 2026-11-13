// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package stacktest runs andante's two services in a test: a real catalogue API
// and a real server API, each with a database of its own, connected by a real
// sync. Crawling is left out, so that a test controls what's indexed.
package stacktest

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/app"
	"github.com/gerrowadat/andante/internal/bootstrap"
	catalogueapi "github.com/gerrowadat/andante/internal/catalogue/api"
	catalogue "github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/clientip"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/sitetest"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/find"
	"github.com/gerrowadat/andante/internal/logring"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/gerrowadat/andante/internal/store/storetest"
	"github.com/prometheus/client_golang/prometheus"
)

// CatalogueToken is the token the test catalogue accepts.
const CatalogueToken = "test-catalogue-token"

// Stack is the two services.
type Stack struct {
	Store     *store.Store
	Catalogue *catalogue.Store
	// CatalogueServer is the catalogue's listener. Close it to take the catalogue down.
	CatalogueServer *httptest.Server
	Client          *replica.Client
	Sync            *replica.Syncer
	Reads           *reads.Service
	App             *app.App
	Registry        *prometheus.Registry
	URL             string
	// Logs and CatalogueLogs are what each service's GET /logs reads. Log to them
	// with Logger.
	Logs, CatalogueLogs *logring.Ring

	lookahead int
}

// Logger logs everything to a ring and nowhere else. slog.DiscardHandler won't do,
// since it says no to every level.
func Logger(ring *logring.Ring) *slog.Logger {
	return slog.New(ring.Handler(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// CatalogueHandler returns the catalogue's API over st, whose GET /logs reads logs.
func CatalogueHandler(t *testing.T, st *catalogue.Store, reg prometheus.Registerer, logs *logring.Ring) http.Handler {
	t.Helper()
	registry := discovery.NewRegistry(strategies.All()...)
	fetcher := discovery.NewFetcher(sitetest.FetcherOptions(sitetest.NewClock()))
	metrics := discovery.NewMetrics(reg)
	prober := discovery.NewProber(registry, fetcher, metrics)
	handler, err := catalogueapi.New(catalogueapi.Options{
		Store: st, Strategies: registry, Prober: prober, Finder: find.New(st, prober, metrics),
		Token: CatalogueToken, Registry: reg, Logger: Logger(logs), ProbeTimeout: time.Minute, Logs: logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// Options change a Stack. The zero value is fine.
type Options struct {
	Store     *store.Store // By default, a new one.
	Lookahead int          // By default, 50.
	Now       func() time.Time
	// BootstrapToken turns on POST /bootstrap with this token.
	BootstrapToken string
}

// New starts both services.
func New(t *testing.T) *Stack { return NewWith(t, Options{}) }

// NewWith starts both services.
func NewWith(t *testing.T, opts Options) *Stack {
	t.Helper()
	s := &Stack{Store: opts.Store, Catalogue: storetest.Catalogue(t), Registry: prometheus.NewRegistry(), lookahead: opts.Lookahead}
	if s.Store == nil {
		s.Store = storetest.New(t)
	}
	if s.lookahead == 0 {
		s.lookahead = 50
	}

	// Each ring counts what's logged, so they can't share a registry.
	s.Logs, s.CatalogueLogs = logring.New(200, s.Registry), logring.New(200, prometheus.NewRegistry())
	mux := http.NewServeMux()
	mux.Handle(catalogueapi.BasePath+"/", CatalogueHandler(t, s.Catalogue, s.Registry, s.CatalogueLogs))
	s.CatalogueServer = httptest.NewServer(mux)
	t.Cleanup(s.CatalogueServer.Close)

	client, err := replica.NewClient(s.CatalogueServer.URL+catalogueapi.BasePath, CatalogueToken, s.CatalogueServer.Client())
	if err != nil {
		t.Fatal(err)
	}
	s.Client = client
	s.Reads = reads.New(reads.Options{Store: s.Store, DefaultTimezone: "UTC", MinDatedShare: 0.9, Now: opts.Now})
	s.Sync = replica.New(replica.Options{
		Store: s.Store, Client: client, Logger: slog.New(slog.DiscardHandler), Registry: s.Registry,
		Furthest: s.Reads.Furthest, DemandInterval: time.Hour,
	})
	clients, err := clientip.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	s.App = &app.App{Store: s.Store, Reads: s.Reads, Catalogue: client, Sync: s.Sync, Logger: Logger(s.Logs), LogRing: s.Logs}
	var setup *bootstrap.Service
	if opts.BootstrapToken != "" {
		setup = bootstrap.New(bootstrap.Options{
			Store: s.Store, Token: opts.BootstrapToken, BaseURL: "https://andante.example/",
			Registry: s.Registry, Logger: slog.New(slog.DiscardHandler), Now: opts.Now,
		})
	}
	handler, err := api.New(api.Options{
		Store: s.Store, Catalogue: client, Sync: s.Sync, Reads: s.Reads, Clients: clients, BaseURL: "https://andante.example/",
		Registry: s.Registry, Logger: Logger(s.Logs), ProbeTimeout: time.Minute, Bootstrap: setup, Logs: s.Logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Settle does what the running services would get round to: it reports demand,
// crawls until there's nothing left to do, and brings the server's copy up to date.
func (s *Stack) Settle(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	ids, err := s.Store.ListSourceIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if err := s.Sync.PushDemand(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	clock := sitetest.NewClock()
	loop := discovery.NewLoop(discovery.LoopOptions{
		Store: s.Catalogue, Registry: discovery.NewRegistry(strategies.All()...), Fetcher: discovery.NewFetcher(sitetest.FetcherOptions(clock)),
		Metrics: discovery.NewMetrics(prometheus.NewRegistry()), Logger: slog.New(slog.DiscardHandler), Clock: clock,
		Lookahead: s.lookahead, TailInterval: 6 * time.Hour, PollInterval: time.Minute, Furthest: s.Catalogue.Furthest,
	})
	for range 50 {
		worked, err := loop.Tick(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if err := s.Sync.CatchUp(ctx); err != nil {
		t.Fatal(err)
	}
}
