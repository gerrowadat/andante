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
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/gerrowadat/andante/internal/discovery"
	"github.com/gerrowadat/andante/internal/discovery/strategies"
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
	apiHandler, err := api.New(api.Options{
		Store:      st,
		Strategies: discovery.NewRegistry(strategies.Crawl{}),
		Registry:   reg,
		Logger:     logger,
	})
	if err != nil {
		return err
	}

	srv, err := server.New(cfg, server.Deps{Ready: st.Ready, Registry: reg, Logger: logger, API: apiHandler})
	if err != nil {
		return err
	}
	return srv.Run(ctx)
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
