// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Command andante-catalogue holds sources and entries, and does all the talking to
// other people's websites. It knows nothing about users or reads, and only
// andante-server calls it.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/buildinfo"
	catalogueapi "github.com/gerrowadat/andante/internal/catalogue/api"
	"github.com/gerrowadat/andante/internal/catalogue/store"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/find"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	// A default command, not root flags, so that the version command and flag work
	// without the settings the service needs.
	Serve struct {
		config.Catalogue `embed:""`
	} `cmd:"" default:"withargs" help:"Run the catalogue (the default)."`
	VersionCmd struct{} `cmd:"" name:"version" help:"Print the version."`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Exit)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, exit func(int)) int {
	var c cli
	parser, err := kong.New(&c,
		kong.Name("andante-catalogue"),
		kong.Description("Indexes serials for andante-server."),
		kong.Vars{"version": buildinfo.String()},
		kong.Writers(stdout, stderr),
		kong.Exit(exit),
	)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	parsed, err := parser.Parse(args)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 2
	}
	if parsed.Command() == "version" {
		_, _ = fmt.Fprintln(stdout, buildinfo.String())
		return 0
	}

	logger := newLogger(c.Serve.LogFormat, c.Serve.LogLevel, stderr)
	if err := serve(ctx, c.Serve.Catalogue, logger); err != nil {
		logger.Error("exiting", "error", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg config.Catalogue, logger *slog.Logger) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "andante_build_info",
		Help: "Always 1; the labels carry the running version and commit.",
	}, []string{"version", "commit"}).WithLabelValues(buildinfo.Version(), buildinfo.Commit()).Set(1)

	registry := discovery.NewRegistry(strategies.All()...)
	metrics := discovery.NewMetrics(reg)
	fetcher := discovery.NewFetcher(discovery.FetcherOptions{
		UserAgent:    cfg.CrawlerUserAgent(),
		Interval:     cfg.CrawlInterval,
		Timeout:      cfg.CrawlRequestTimeout,
		BackoffCap:   cfg.CrawlBackoffCap,
		RobotsTTL:    cfg.RobotsTTL,
		MaxBody:      cfg.CrawlMaxBody,
		AllowPrivate: cfg.CrawlAllowPrivate,
	})
	// The crawl and the API stop together: a crawl never holds up the API, and the API
	// ending for any reason ends the crawl.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	prober := discovery.NewProber(registry, fetcher, metrics)
	apiHandler, err := catalogueapi.New(catalogueapi.Options{
		Finder:   find.New(st, prober, metrics),
		Stopping: ctx,
		Store:    st, Strategies: registry, Prober: prober,
		Token: cfg.APIToken, Registry: reg, Logger: logger, ProbeTimeout: cfg.ProbeTimeout,
	})
	if err != nil {
		return err
	}
	loop := discovery.NewLoop(discovery.LoopOptions{
		Store:        st,
		Registry:     registry,
		Fetcher:      fetcher,
		Metrics:      metrics,
		Logger:       logger,
		Lookahead:    cfg.CrawlLookahead,
		TailInterval: cfg.TailInterval,
		PollInterval: cfg.CrawlPollInterval,
		Furthest:     st.Furthest,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ready, cancel := context.WithTimeout(r.Context(), cfg.ReadinessTimeout)
		defer cancel()
		if err := st.Ready(ready); err != nil {
			logger.Warn("not ready", "error", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	mux.Handle(catalogueapi.BasePath+"/", apiHandler)

	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		if err := loop.Run(ctx); err != nil {
			logger.Error("crawl loop stopped", "error", err)
		}
	})
	failed := make(chan error, 1)
	wg.Go(func() {
		if err := server.Serve(listener); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	})
	logger.Info("listening", "address", listener.Addr().String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failed:
	}
	shutdown, stop := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer stop()
	logger.Info("shutting down")
	cancel()
	err = errors.Join(serveErr, server.Shutdown(shutdown))
	wg.Wait()
	return err
}

func newLogger(format, level string, w io.Writer) *slog.Logger {
	var l slog.Level
	// config.Catalogue restricts level to names slog accepts.
	_ = l.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: l}
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
