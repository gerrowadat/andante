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
	"time"

	"github.com/alecthomas/kong"
	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/gerrowadat/andante/internal/reads"
	"github.com/gerrowadat/andante/internal/replica"
	"github.com/gerrowadat/andante/internal/seal"
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
	VersionCmd struct{}  `cmd:"" name:"version" help:"Print the version."`
	User       userCmd   `cmd:"" help:"Manage users."`
	Rotate     rotateCmd `cmd:"" name:"rotate-secret" help:"Reseal everything under a new secret."`
}

type userCmd struct {
	DatabaseURL string `name:"database-url" env:"ANDANTE_DATABASE_URL" required:"" help:"Postgres DSN."`

	Add struct {
		Name  string `arg:"" help:"A short name, in lower case."`
		Admin bool   `help:"Can change sources, and see everyone's reads."`
	} `cmd:"" help:"Add a user, and print their first API token."`
	Reset struct {
		Name string `arg:""`
	} `cmd:"" help:"Revoke a user's API tokens, and print a new one."`
}

type rotateCmd struct {
	DatabaseURL string `name:"database-url" env:"ANDANTE_DATABASE_URL" required:"" help:"Postgres DSN."`
	Secret      string `name:"secret" env:"ANDANTE_SECRET" help:"The current secret."`
	NewSecret   string `name:"new-secret" env:"ANDANTE_NEW_SECRET" required:"" help:"The new secret."`
	ForgetURLs  bool   `name:"forget-urls" help:"The current secret is lost. Tokens keep working, but their URLs can't be shown again."`
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

	switch parsed.Command() {
	case "user add <name>":
		role := store.RoleUser
		if c.User.Add.Admin {
			role = store.RoleAdmin
		}
		return withStore(ctx, c.User.DatabaseURL, stderr, func(st *store.Store) error {
			_, token, err := st.CreateUser(ctx, c.User.Add.Name, role)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, token)
			return nil
		})
	case "user reset <name>":
		return withStore(ctx, c.User.DatabaseURL, stderr, func(st *store.Store) error {
			token, err := st.ResetUser(ctx, c.User.Reset.Name)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, token)
			return nil
		})
	case "rotate-secret":
		return withStore(ctx, c.Rotate.DatabaseURL, stderr, func(st *store.Store) error {
			return rotate(ctx, st, c.Rotate)
		})
	}

	logger := newLogger(c.Serve.LogFormat, c.Serve.LogLevel, stderr)
	if err := serve(ctx, c.Serve.Config, logger); err != nil {
		logger.Error("exiting", "error", err)
		return 1
	}
	return 0
}

// withStore runs a command against a migrated store. A command can come before the
// server's first start, so it migrates too.
func withStore(ctx context.Context, dsn string, stderr io.Writer, run func(*store.Store) error) int {
	st, err := store.Open(ctx, dsn)
	if err == nil {
		defer st.Close()
		if err = st.Migrate(ctx); err == nil {
			err = run(st)
		}
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func rotate(ctx context.Context, st *store.Store, cmd rotateCmd) error {
	to, err := seal.New(cmd.NewSecret)
	if err != nil {
		return err
	}
	if cmd.ForgetURLs {
		return st.ForgetSecrets(ctx, to)
	}
	from, err := seal.New(cmd.Secret)
	if err != nil {
		return err
	}
	return st.RotateSecret(ctx, from, to)
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
	box, err := seal.New(cfg.Secret)
	if err != nil {
		return err
	}
	if err := st.UseBox(ctx, box); err != nil {
		return err
	}
	users, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if users == 0 {
		logger.Warn("there are no users yet, so nothing can use the API; run andante-server user add <name> --admin")
	}

	if cfg.TestClockOffset != 0 {
		logger.Warn("running ahead of the real clock, which is only for tests", "offset", cfg.TestClockOffset)
	}
	now := func() time.Time { return time.Now().Add(cfg.TestClockOffset) }

	reg := prometheus.NewRegistry()
	readService := reads.New(reads.Options{
		Store:           st,
		DefaultTimezone: cfg.DefaultTimezone,
		MinDatedShare:   cfg.ReplayMinDatedShare,
		Now:             now,
	})
	catalogue, err := replica.NewClient(cfg.CatalogueURL, cfg.CatalogueToken, nil)
	if err != nil {
		return err
	}
	syncer := replica.New(replica.Options{
		Store: st, Client: catalogue, Logger: logger, Registry: reg,
		Furthest: readService.Furthest, DemandInterval: cfg.DemandInterval,
	})
	apiHandler, err := api.New(api.Options{
		Store:        st,
		Catalogue:    catalogue,
		Sync:         syncer,
		Reads:        readService,
		BaseURL:      cfg.BaseURL,
		Registry:     reg,
		Logger:       logger,
		ProbeTimeout: cfg.ProbeTimeout,
	})
	if err != nil {
		return err
	}

	public := server.NewPublic(server.PublicOptions{
		Store:       st,
		Reads:       readService,
		BaseURL:     cfg.BaseURL,
		RatePerHour: cfg.FeedRateLimit,
		Registry:    reg,
		Logger:      logger,
		Now:         now,
	})
	srv, err := server.New(cfg, server.Deps{Ready: st.Ready, Registry: reg, Logger: logger, API: apiHandler, Public: public})
	if err != nil {
		return err
	}

	// The two stop together. Syncing never holds up serving, and serving ending for
	// any reason ends the sync.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() { syncer.Run(ctx) })
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
