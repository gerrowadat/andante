// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Command andante-server is the andante server.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/server"
	"github.com/gerrowadat/andante/internal/store"
	"github.com/prometheus/client_golang/prometheus"

	// Embedded because the distroless image has no zoneinfo, and schedules
	// are evaluated in IANA time zones.
	_ "time/tzdata"
)

type cli struct {
	Version kong.VersionFlag `help:"Print the version and exit."`

	// A default command, not root flags, so that the version command and flag
	// work without the settings the server needs.
	Serve struct {
		config.Config `embed:""`
	} `cmd:"" default:"withargs" help:"Run the server (the default)."`
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
		kong.Name("andante-server"),
		kong.Description("Replays a serial's archive as a paced feed."),
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
	if err := serve(ctx, c.Serve.Config, logger); err != nil {
		logger.Error("exiting", "error", err)
		return 1
	}
	return 0
}

func serve(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		return err
	}
	generated, err := st.BootstrapAPIToken(ctx, cfg.AdminToken)
	if err != nil {
		return err
	}
	if generated != "" {
		logger.Warn("no API tokens existed, so one was generated; it is shown only this once", "token", generated)
	}

	reg := prometheus.NewRegistry()
	registry := discovery.NewRegistry(strategies.Crawl{})
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
	readService := reads.New(reads.Options{
		Store:           st,
		DefaultTimezone: cfg.DefaultTimezone,
		MinDatedShare:   cfg.ReplayMinDatedShare,
	})
	apiHandler, err := api.New(api.Options{
		Store:        st,
		Strategies:   registry,
		Prober:       discovery.NewProber(registry, fetcher, metrics),
		Reads:        readService,
		Registry:     reg,
		Logger:       logger,
		ProbeTimeout: cfg.ProbeTimeout,
	})
	if err != nil {
		return err
	}

	srv, err := server.New(cfg, server.Deps{Ready: st.Ready, Registry: reg, Logger: logger, API: apiHandler})
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
		Furthest:     readService.Furthest,
	})

	// The two stop together. A crawl never holds up serving, and serving ending
	// for any reason ends the crawl.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		if err := loop.Run(ctx); err != nil {
			logger.Error("crawl loop stopped", "error", err)
		}
	})
	err = srv.Run(ctx)
	cancel()
	wg.Wait()
	return err
}

func newLogger(format, level string, w io.Writer) *slog.Logger {
	var l slog.Level
	// config.Config restricts level to names slog accepts.
	_ = l.UnmarshalText([]byte(level))
	opts := &slog.HandlerOptions{Level: l}
	if format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}
