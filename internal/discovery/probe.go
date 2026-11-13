// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// Fetcher returns the fetcher probes use, for anything else that isn't a crawl.
func (p *Prober) Fetcher() PageFetcher { return probeFetcher{p: p} }

// probeFetcher collects a strategy's warning notes into warnings, if it has
// somewhere to put them.
type probeFetcher struct {
	p        *Prober
	warnings *[]string
}

func (f probeFetcher) Fetch(ctx context.Context, url string) (*Response, error) {
	return f.count(f.p.fetcher.Fetch(ctx, url, Validators{}))
}

// Revalidate fetches. A probe has nothing earlier to compare with.
func (f probeFetcher) Revalidate(ctx context.Context, url string) (*Response, error) {
	return f.Fetch(ctx, url)
}

func (f probeFetcher) Head(ctx context.Context, url string) (*Response, error) {
	return f.count(f.p.fetcher.Head(ctx, url))
}

// Note implements PageFetcher. A probe shows a person the warnings, and keeps no
// crawl log.
func (f probeFetcher) Note(_ context.Context, note Note) {
	if f.warnings != nil && note.Level >= slog.LevelWarn {
		*f.warnings = append(*f.warnings, note.Message)
	}
}

func (f probeFetcher) count(resp *Response, err error) (*Response, error) {
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
		var noted []string
		batch, err := strategy.Next(ctx, probeFetcher{p: p, warnings: &noted}, src, cursor, req.Limit-len(result.Entries))
		result.Warnings = append(result.Warnings, noted...)
		if err != nil {
			if len(result.Entries) == 0 || errors.Is(err, ErrCycle) {
				return ProbeResult{}, err
			}
			result.Warnings = append(result.Warnings, "stopped early: "+err.Error())
			break
		}
		for _, e := range batch.Entries {
			// Feed items can share a link, such as a podcast's home page, and
			// are told apart by their guid.
			key := e.GUID
			if key == "" {
				key = e.URL
			}
			if seen[key] {
				return ProbeResult{}, fmt.Errorf("%w: back at %s", ErrCycle, key)
			}
			seen[key] = true
		}
		// A feed's strategy returns the whole archive at once, because it has
		// already had to fetch all of it.
		room := min(req.Limit-len(result.Entries), len(batch.Entries))
		result.Entries = append(result.Entries, batch.Entries[:room]...)
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
