// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/gerrowadat/andante/internal/api"
	"github.com/gerrowadat/andante/internal/buildinfo"
	"github.com/gerrowadat/andante/internal/config"
	"github.com/gerrowadat/andante/internal/rootpath"
	"github.com/gerrowadat/andante/internal/web"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ReadyFunc reports whether the server can serve, by returning nil.
type ReadyFunc func(context.Context) error

// Deps are what the server is built from.
type Deps struct {
	Ready    ReadyFunc
	Registry *prometheus.Registry
	Logger   *slog.Logger
	// API is mounted at api.BasePath on the public listener. It may be nil.
	API http.Handler
	// Web is the website, mounted at web.Paths on the public listener. It may be nil.
	Web http.Handler
	// Public serves the rest of the public listener. With none, it answers 404.
	Public http.Handler
}

// Server holds andante's two listeners. The public one has the feeds and the API,
// which needs a token. Health and metrics have no auth, so they're only ever on the
// ops listener.
type Server struct {
	logger      *slog.Logger
	cfg         config.Config
	public, ops *listener
}

type listener struct {
	net  net.Listener
	http *http.Server
}

// New binds both listeners. Run serves them.
func New(cfg config.Config, deps Deps) (*Server, error) {
	logger := deps.Logger
	var publicHandler http.Handler = http.NewServeMux()
	if deps.Public != nil {
		publicHandler = deps.Public
	}
	if deps.API != nil || deps.Web != nil {
		mux := http.NewServeMux()
		mux.Handle("/", publicHandler)
		if deps.API != nil {
			mux.Handle(api.BasePath+"/", deps.API)
		}
		if deps.Web != nil {
			for _, path := range web.Paths {
				mux.Handle(path, deps.Web)
			}
		}
		publicHandler = mux
	}
	// Validate has already checked it.
	root, _ := rootpath.FromBaseURL(cfg.BaseURL)
	public, err := newListener(cfg, cfg.Listen, rootpath.Mount(root, publicHandler), logger)
	if err != nil {
		return nil, fmt.Errorf("public listener: %w", err)
	}
	ops, err := newListener(cfg, cfg.OpsListen, rootpath.Mount(root, opsMux(cfg, deps)), logger)
	if err != nil {
		_ = public.net.Close()
		return nil, fmt.Errorf("ops listener: %w", err)
	}
	return &Server{logger: logger, cfg: cfg, public: public, ops: ops}, nil
}

func newListener(cfg config.Config, addr string, handler http.Handler, logger *slog.Logger) (*listener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &listener{net: l, http: &http.Server{
		Handler:           logRequests(logger, recoverPanics(logger, handler)),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}}, nil
}

func opsMux(cfg config.Config, deps Deps) *http.ServeMux {
	reg := deps.Registry
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	promauto.With(reg).NewGaugeVec(prometheus.GaugeOpts{
		Name: "andante_build_info",
		Help: "Always 1; the labels carry the running version and commit.",
	}, []string{"version", "commit"}).WithLabelValues(buildinfo.Version(), buildinfo.Commit()).Set(1)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), cfg.ReadinessTimeout)
		defer cancel()
		if err := deps.Ready(ctx); err != nil {
			deps.Logger.Warn("not ready", "error", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprintln(w, "ok")
	})
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	return mux
}

// PublicAddr returns the address the public listener is bound to.
func (s *Server) PublicAddr() net.Addr { return s.public.net.Addr() }

// OpsAddr returns the address the ops listener is bound to.
func (s *Server) OpsAddr() net.Addr { return s.ops.net.Addr() }

// Run serves until ctx ends or a listener fails, then shuts down gracefully.
func (s *Server) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	failed := make(chan error, 2)
	for _, l := range []*listener{s.public, s.ops} {
		wg.Go(func() {
			if err := l.http.Serve(l.net); !errors.Is(err, http.ErrServerClosed) {
				failed <- err
			}
		})
	}
	s.logger.Info("listening", "public", s.PublicAddr().String(), "ops", s.OpsAddr().String())

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-failed:
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()
	s.logger.Info("shutting down")
	err := errors.Join(serveErr, s.public.http.Shutdown(shutdownCtx), s.ops.http.Shutdown(shutdownCtx))
	wg.Wait()
	return err
}
