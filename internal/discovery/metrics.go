// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// probeSource labels a probe's requests. It cannot be a slug, because slugs do
// not start with an underscore.
const probeSource = "_probe"

// Metrics are discovery's Prometheus metrics, shared by the loop and probes.
type Metrics struct {
	requests  *prometheus.CounterVec
	duration  prometheus.Histogram
	entries   *prometheus.CounterVec
	state     *prometheus.GaugeVec
	probeRuns *prometheus.CounterVec
	finds     *prometheus.CounterVec
}

// NewMetrics registers discovery's metrics.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	factory := promauto.With(reg)
	return &Metrics{
		requests: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_crawl_requests_total",
			Help: "Requests to other people's sites, by source and status.",
		}, []string{"source", "status"}),
		duration: factory.NewHistogram(prometheus.HistogramOpts{
			Name:    "andante_crawl_duration_seconds",
			Help:    "How long each crawl request took.",
			Buckets: prometheus.DefBuckets,
		}),
		entries: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_entries_discovered_total",
			Help: "Entries added to the index, by source.",
		}, []string{"source"}),
		state: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "andante_source_state",
			Help: "1 for each source's current state, 0 for the others.",
		}, []string{"source", "state"}),
		finds: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_discoveries_total",
			Help: "Attempts to work out how to read a site, by result.",
		}, []string{"result"}),
		probeRuns: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "andante_probe_runs_total",
			Help: "Probes, by result.",
		}, []string{"result"}),
	}
}

// CountFind records how an attempt to read a site went.
func (m *Metrics) CountFind(result string) { m.finds.WithLabelValues(result).Inc() }
