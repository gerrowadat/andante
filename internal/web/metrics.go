// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import (
	"context"
	"log/slog"
	"time"

	"github.com/gerrowadat/andante/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// sessionsTimeout bounds the count a scrape waits for.
const sessionsTimeout = 5 * time.Second

// sessions counts live sessions when Prometheus asks. Sessions end by expiring as well
// as by logging out, so a gauge kept up to date by hand would drift.
type sessions struct {
	store  *store.Store
	now    func() time.Time
	logger *slog.Logger
	desc   *prometheus.Desc
}

func newSessions(opts Options) *sessions {
	return &sessions{
		store: opts.Store, now: opts.Now, logger: opts.Logger,
		desc: prometheus.NewDesc("andante_sessions", "Live website sessions, by how they logged in: password or oidc.", []string{"method"}, nil),
	}
}

func (c *sessions) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c *sessions) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), sessionsTimeout)
	defer cancel()
	counts, err := c.store.CountSessions(ctx, c.now())
	if err != nil {
		// Leave it out rather than fail the scrape, and so every other metric with it.
		c.logger.Warn("counting sessions for metrics", "error", err)
		return
	}
	for _, method := range []string{store.MethodPassword, store.MethodOIDC} {
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, float64(counts[method]), method)
	}
}
