// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// sharedImageShare is how many of a probe's pages must show one image before it
// looks like a site logo and not the content. It needs a few pages to mean much.
const (
	sharedImageShare = 0.9
	sharedImageMin   = 3
)

// ProbeRequest is a proposed source to try.
type ProbeRequest struct {
	Strategy string
	Config   json.RawMessage
	Kind     string
	Limit    int
}

// ProbeResult is what discovery found. Nothing is saved.
type ProbeResult struct {
	Entries    []Entry
	Warnings   []string
	DatedShare float64
}

// Prober runs dry-run discovery.
type Prober struct {
	registry *Registry
	fetcher  *Fetcher
	metrics  *Metrics
}

// NewProber returns a Prober.
func NewProber(registry *Registry, fetcher *Fetcher, metrics *Metrics) *Prober {
	return &Prober{registry: registry, fetcher: fetcher, metrics: metrics}
}

type probeFetcher struct{ p *Prober }

func (f probeFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	resp, err := f.p.fetcher.Fetch(ctx, url, Validators{})
	label := errorLabel(err)
	if resp != nil {
		label = strconv.Itoa(resp.Status)
	}
	f.p.metrics.requests.WithLabelValues(probeSource, label).Inc()
	return resp, err
}

// Probe runs a strategy for up to req.Limit entries, without saving anything.
func (p *Prober) Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	result, err := p.run(ctx, req)
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	p.metrics.probeRuns.WithLabelValues(outcome).Inc()
	return result, err
}

func (p *Prober) run(ctx context.Context, req ProbeRequest) (ProbeResult, error) {
	strategy, err := p.registry.Get(req.Strategy)
	if err != nil {
		return ProbeResult{}, err
	}
	src := Source{DefaultKind: req.Kind, Config: req.Config}

	var (
		result ProbeResult
		cursor Cursor
		seen   = map[string]bool{}
	)
	for len(result.Entries) < req.Limit {
		batch, err := strategy.Next(ctx, probeFetcher{p}, src, cursor, req.Limit-len(result.Entries))
		if err != nil {
			if len(result.Entries) == 0 || errors.Is(err, ErrCycle) {
				return ProbeResult{}, err
			}
			result.Warnings = append(result.Warnings, "stopped early: "+err.Error())
			break
		}
		for _, e := range batch.Entries {
			if seen[e.URL] {
				return ProbeResult{}, fmt.Errorf("%w: back at %s", ErrCycle, e.URL)
			}
			seen[e.URL] = true
		}
		result.Entries = append(result.Entries, batch.Entries...)
		result.Warnings = append(result.Warnings, batch.Warnings...)
		if batch.LiveEnd || len(batch.Entries) == 0 {
			break
		}
		cursor = batch.Cursor
	}
	result.DatedShare = datedShare(result.Entries)
	result.Warnings = append(result.Warnings, sharedImageWarnings(result.Entries)...)
	return result, nil
}

func datedShare(entries []Entry) float64 {
	if len(entries) == 0 {
		return 0
	}
	dated := 0
	for _, e := range entries {
		if e.Date != nil {
			dated++
		}
	}
	return float64(dated) / float64(len(entries))
}

func sharedImageWarnings(entries []Entry) []string {
	if len(entries) < sharedImageMin {
		return nil
	}
	counts := map[string]int{}
	for _, e := range entries {
		for _, image := range e.Images {
			counts[image]++
		}
	}
	var warnings []string
	for image, count := range counts {
		if float64(count)/float64(len(entries)) >= sharedImageShare {
			warnings = append(warnings, fmt.Sprintf("image %s is on %d of %d pages, so it may be a site logo", image, count, len(entries)))
		}
	}
	return warnings
}
